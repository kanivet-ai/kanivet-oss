package finops

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PricingSource string

const (
	PricingSourceCUR      PricingSource = "cur"
	PricingSourceAWSAPI   PricingSource = "aws-api"
	PricingSourceAzureAPI PricingSource = "azure-api"
	PricingSourceUnknown  PricingSource = "unknown"
)

type PricingStatus struct {
	Source        PricingSource `json:"source"`
	LastUpdated   time.Time     `json:"lastUpdated"`
	InstanceCount int           `json:"instanceCount"`
	IsAvailable   bool          `json:"isAvailable"`
	Error         string        `json:"error,omitempty"`
}

type PricingProvider interface {
	GetInstancePricing(instanceType, region string) (*InstancePricing, error)
	GetSpotPricing(instanceType, region, az string) (float64, error)
	RefreshPricing() error
	GetStatus() PricingStatus
}

type AWSPricingProvider struct {
	mu              sync.RWMutex
	priceCache      map[string]*InstancePricing
	regionDataCache map[string]*regionPricingData
	lastRefresh     time.Time
	refreshPeriod   time.Duration
	lastError       string
	pendingRegions  map[string]chan struct{}
	pendingMu       sync.Mutex
	cacheDir        string
}

type regionPricingData struct {
	products map[string]map[string]interface{}
	terms    map[string]interface{}
	loaded   time.Time
}

func NewAWSPricingProvider() *AWSPricingProvider {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".kanivet", "pricing-cache")
	os.MkdirAll(cacheDir, 0755)
	p := &AWSPricingProvider{
		priceCache:      make(map[string]*InstancePricing),
		regionDataCache: make(map[string]*regionPricingData),
		// EC2 on-demand pricing changes on the order of quarters; anything
		// under a day of staleness is irrelevant for cost dashboards. The
		// disk cache itself expires after 7 days.
		refreshPeriod:  24 * time.Hour,
		pendingRegions: make(map[string]chan struct{}),
		cacheDir:       cacheDir,
	}
	p.loadAllCachedRegions()
	return p
}

type diskPricingCache struct {
	Region  string                      `json:"region"`
	SavedAt time.Time                   `json:"savedAt"`
	Pricing map[string]*InstancePricing `json:"pricing"`
}

func (p *AWSPricingProvider) loadAllCachedRegions() {
	files, err := filepath.Glob(filepath.Join(p.cacheDir, "aws-*.json.gz"))
	if err != nil {
		return
	}
	for _, file := range files {
		p.loadCacheFromDisk(file)
	}
	if len(p.priceCache) > 0 {
		log.Printf("[AWS Pricing] Loaded %d cached instance types from disk", len(p.priceCache))
	}
}

func (p *AWSPricingProvider) loadCacheFromDisk(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return
	}
	defer gz.Close()

	var cache diskPricingCache
	cacheData, err := io.ReadAll(gz)
	if err != nil {
		return
	}
	if err := jsonv2.Unmarshal(cacheData, &cache); err != nil {
		return
	}

	if time.Since(cache.SavedAt) > 7*24*time.Hour {
		os.Remove(filePath)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for key, pricing := range cache.Pricing {
		p.priceCache[key] = pricing
	}
	p.regionDataCache[cache.Region] = &regionPricingData{loaded: cache.SavedAt}
	if cache.SavedAt.After(p.lastRefresh) {
		p.lastRefresh = cache.SavedAt
	}
}

func (p *AWSPricingProvider) saveCacheToDisk(region string) {
	p.mu.RLock()
	regionPricing := make(map[string]*InstancePricing)
	prefix := region + ":"
	for key, pricing := range p.priceCache {
		if strings.HasPrefix(key, prefix) {
			regionPricing[key] = pricing
		}
	}
	p.mu.RUnlock()

	if len(regionPricing) == 0 {
		return
	}

	cache := diskPricingCache{
		Region:  region,
		SavedAt: time.Now(),
		Pricing: regionPricing,
	}

	filePath := filepath.Join(p.cacheDir, fmt.Sprintf("aws-%s.json.gz", region))
	file, err := os.Create(filePath)
	if err != nil {
		log.Printf("[AWS Pricing] Failed to create cache file: %v", err)
		return
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()

	data, err := jsonv2.Marshal(cache)
	if err != nil {
		log.Printf("[AWS Pricing] Failed to marshal cache: %v", err)
		return
	}
	if _, err := gz.Write(data); err != nil {
		log.Printf("[AWS Pricing] Failed to write cache: %v", err)
		return
	}
	log.Printf("[AWS Pricing] Saved %d instance types for %s to disk", len(regionPricing), region)
}

func (p *AWSPricingProvider) GetStatus() PricingStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return PricingStatus{
		Source:        PricingSourceAWSAPI,
		LastUpdated:   p.lastRefresh,
		InstanceCount: len(p.priceCache),
		IsAvailable:   len(p.priceCache) > 0 || p.lastRefresh.IsZero(),
		Error:         p.lastError,
	}
}

func (p *AWSPricingProvider) GetInstancePricing(instanceType, region string) (*InstancePricing, error) {
	p.mu.RLock()
	key := fmt.Sprintf("%s:%s", region, instanceType)
	if pricing, ok := p.priceCache[key]; ok {
		p.mu.RUnlock()
		return pricing, nil
	}
	p.mu.RUnlock()

	if err := p.ensureRegionLoaded(region); err != nil {
		return nil, fmt.Errorf("failed to load region pricing: %w", err)
	}

	p.mu.RLock()
	if pricing, ok := p.priceCache[key]; ok {
		p.mu.RUnlock()
		return pricing, nil
	}
	p.mu.RUnlock()

	return nil, fmt.Errorf("pricing not found for %s in %s", instanceType, region)
}

// GetInstancePricingCached serves whatever is already in memory and never
// blocks on the offer-file download: a missing or stale region triggers a
// background singleflight load instead. Callers render "pricing missing" and
// pick up real prices on a later poll.
func (p *AWSPricingProvider) GetInstancePricingCached(instanceType, region string) (*InstancePricing, error) {
	p.mu.RLock()
	key := fmt.Sprintf("%s:%s", region, instanceType)
	pricing, ok := p.priceCache[key]
	data, hasRegion := p.regionDataCache[region]
	p.mu.RUnlock()
	if !hasRegion || time.Since(data.loaded) >= p.refreshPeriod {
		go func() { _ = p.loadRegionSingleflight(region) }()
	}
	if ok {
		return pricing, nil
	}
	return nil, fmt.Errorf("pricing not loaded yet for %s in %s", instanceType, region)
}

// RegionsLoaded reports whether every region already has pricing data, fresh
// or stale — stale is fine to serve while a background refresh runs.
func (p *AWSPricingProvider) RegionsLoaded(regions []string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, r := range regions {
		if _, ok := p.regionDataCache[r]; !ok {
			return false
		}
	}
	return true
}

func (p *AWSPricingProvider) ensureRegionLoaded(region string) error {
	p.mu.RLock()
	data, ok := p.regionDataCache[region]
	p.mu.RUnlock()
	if ok && time.Since(data.loaded) < p.refreshPeriod {
		return nil
	}
	if ok {
		// Stale but present (e.g. day-old disk cache): serve the existing
		// prices NOW and refresh in the background. EC2 pricing changes
		// rarely - blocking a dashboard request on the multi-hundred-MB
		// offers download for slightly-old prices made FinOps take minutes
		// to open every time the freshness window lapsed.
		go func() { _ = p.loadRegionSingleflight(region) }()
		return nil
	}
	// No data at all for this region - the very first load has to block,
	// there is nothing to render prices from yet.
	return p.loadRegionSingleflight(region)
}

// loadRegionSingleflight collapses concurrent loads of the same region into
// one download; followers wait for the leader.
func (p *AWSPricingProvider) loadRegionSingleflight(region string) error {
	p.pendingMu.Lock()
	if ch, ok := p.pendingRegions[region]; ok {
		p.pendingMu.Unlock()
		<-ch
		return nil
	}
	ch := make(chan struct{})
	p.pendingRegions[region] = ch
	p.pendingMu.Unlock()

	defer func() {
		p.pendingMu.Lock()
		delete(p.pendingRegions, region)
		close(ch)
		p.pendingMu.Unlock()
	}()

	return p.loadRegionPricing(region)
}

func (p *AWSPricingProvider) loadRegionPricing(region string) error {
	log.Printf("Loading AWS pricing data for region %s...", region)
	start := time.Now()

	url := fmt.Sprintf(
		"https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/%s/index.json",
		region,
	)

	client := &http.Client{Timeout: 900 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		p.mu.Lock()
		p.lastError = err.Error()
		p.mu.Unlock()
		return fmt.Errorf("failed to fetch pricing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("pricing API returned status %d", resp.StatusCode)
		p.mu.Lock()
		p.lastError = errMsg
		p.mu.Unlock()
		return errors.New(errMsg)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.mu.Lock()
		p.lastError = err.Error()
		p.mu.Unlock()
		return fmt.Errorf("failed to read pricing data: %w", err)
	}
	log.Printf("Downloaded %d MB of pricing data for %s", len(body)/(1024*1024), region)

	var data map[string]interface{}
	if err := jsonv2.Unmarshal(body, &data); err != nil {
		p.mu.Lock()
		p.lastError = fmt.Sprintf("JSON parse error (got %d bytes): %v", len(body), err)
		p.mu.Unlock()
		return fmt.Errorf("failed to parse pricing JSON (got %d bytes): %w", len(body), err)
	}

	products, ok := data["products"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid pricing data structure")
	}

	terms, ok := data["terms"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid pricing terms structure")
	}

	onDemand, _ := terms["OnDemand"].(map[string]interface{})
	regionName := getAWSRegionName(region)

	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	for sku, product := range products {
		prod, ok := product.(map[string]interface{})
		if !ok {
			continue
		}
		attrs, ok := prod["attributes"].(map[string]interface{})
		if !ok {
			continue
		}

		if attrs["location"] != regionName {
			continue
		}
		if attrs["operatingSystem"] != "Linux" {
			continue
		}
		if attrs["tenancy"] != "Shared" {
			continue
		}
		if attrs["preInstalledSw"] != "NA" {
			continue
		}
		if attrs["capacitystatus"] != "Used" {
			continue
		}

		instanceType, ok := attrs["instanceType"].(string)
		if !ok || instanceType == "" {
			continue
		}

		vcpu, _ := strconv.Atoi(fmt.Sprintf("%v", attrs["vcpu"]))
		memStr := fmt.Sprintf("%v", attrs["memory"])
		memStr = strings.TrimSuffix(memStr, " GiB")
		memStr = strings.ReplaceAll(memStr, ",", "")
		memGB, _ := strconv.ParseFloat(memStr, 64)

		price := 0.0
		if skuTerms, ok := onDemand[sku].(map[string]interface{}); ok {
			for _, term := range skuTerms {
				termMap, ok := term.(map[string]interface{})
				if !ok {
					continue
				}
				priceDimensions, ok := termMap["priceDimensions"].(map[string]interface{})
				if !ok {
					continue
				}
				for _, pd := range priceDimensions {
					pdMap, ok := pd.(map[string]interface{})
					if !ok {
						continue
					}
					pricePerUnit, ok := pdMap["pricePerUnit"].(map[string]interface{})
					if !ok {
						continue
					}
					if usd, ok := pricePerUnit["USD"].(string); ok {
						price, _ = strconv.ParseFloat(usd, 64)
						break
					}
				}
			}
		}

		if price == 0 || vcpu == 0 {
			continue
		}

		cpuRate, memRate := calculateResourceRates(price, vcpu, memGB)
		key := fmt.Sprintf("%s:%s", region, instanceType)

		p.priceCache[key] = &InstancePricing{
			InstanceType:     instanceType,
			Region:           region,
			OnDemandPrice:    price,
			CPUCores:         vcpu,
			MemoryGB:         memGB,
			CPUHourlyRate:    cpuRate,
			MemoryHourlyRate: memRate,
			LastUpdated:      time.Now(),
		}
		count++
	}

	p.regionDataCache[region] = &regionPricingData{loaded: time.Now()}
	p.lastRefresh = time.Now()
	p.lastError = ""

	log.Printf("Loaded %d instance types for region %s in %v", count, region, time.Since(start))

	go p.saveCacheToDisk(region)
	return nil
}

func (p *AWSPricingProvider) GetSpotPricing(instanceType, region, az string) (float64, error) {
	return 0, fmt.Errorf("spot pricing requires AWS SDK with EC2 API access")
}

func (p *AWSPricingProvider) RefreshPricing() error {
	p.mu.Lock()
	regions := make([]string, 0, len(p.regionDataCache))
	for region := range p.regionDataCache {
		regions = append(regions, region)
	}
	p.mu.Unlock()

	for _, region := range regions {
		if err := p.loadRegionPricing(region); err != nil {
			log.Printf("Failed to refresh pricing for region %s: %v", region, err)
		}
	}
	return nil
}

func calculateResourceRates(totalPrice float64, vcpu int, memGB float64) (cpuRate, memRate float64) {
	cpuRatio := 0.70
	memRatio := 0.30

	if memGB/float64(vcpu) > 6 {
		cpuRatio = 0.50
		memRatio = 0.50
	} else if memGB/float64(vcpu) < 2.5 {
		cpuRatio = 0.80
		memRatio = 0.20
	}

	if vcpu > 0 {
		cpuRate = (totalPrice * cpuRatio) / float64(vcpu)
	}
	if memGB > 0 {
		memRate = (totalPrice * memRatio) / memGB
	}

	return cpuRate, memRate
}

func getAWSRegionName(region string) string {
	regionNames := map[string]string{
		"us-east-1":      "US East (N. Virginia)",
		"us-east-2":      "US East (Ohio)",
		"us-west-1":      "US West (N. California)",
		"us-west-2":      "US West (Oregon)",
		"eu-west-1":      "EU (Ireland)",
		"eu-west-2":      "EU (London)",
		"eu-west-3":      "EU (Paris)",
		"eu-central-1":   "EU (Frankfurt)",
		"eu-north-1":     "EU (Stockholm)",
		"ap-northeast-1": "Asia Pacific (Tokyo)",
		"ap-northeast-2": "Asia Pacific (Seoul)",
		"ap-northeast-3": "Asia Pacific (Osaka)",
		"ap-southeast-1": "Asia Pacific (Singapore)",
		"ap-southeast-2": "Asia Pacific (Sydney)",
		"ap-south-1":     "Asia Pacific (Mumbai)",
		"sa-east-1":      "South America (Sao Paulo)",
		"ca-central-1":   "Canada (Central)",
	}

	if name, ok := regionNames[region]; ok {
		return name
	}
	return region
}

type CURPricingProvider struct {
	mu           sync.RWMutex
	priceCache   map[string]*InstancePricing
	curDataPath  string
	lastLoaded   time.Time
	loadInterval time.Duration
	lastError    string
}

type CURConfig struct {
	DataPath     string
	S3Bucket     string
	S3Prefix     string
	AWSProfile   string
	RefreshHours int
}

func NewCURPricingProvider(config CURConfig) *CURPricingProvider {
	p := &CURPricingProvider{
		priceCache:   make(map[string]*InstancePricing),
		curDataPath:  config.DataPath,
		loadInterval: time.Duration(config.RefreshHours) * time.Hour,
	}
	if config.RefreshHours == 0 {
		p.loadInterval = 24 * time.Hour
	}
	go p.loadCURData(context.Background())
	return p
}

func (p *CURPricingProvider) GetStatus() PricingStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return PricingStatus{
		Source:        PricingSourceCUR,
		LastUpdated:   p.lastLoaded,
		InstanceCount: len(p.priceCache),
		IsAvailable:   len(p.priceCache) > 0,
		Error:         p.lastError,
	}
}

func (p *CURPricingProvider) loadCURData(ctx context.Context) {
	if p.curDataPath == "" {
		homeDir, _ := os.UserHomeDir()
		p.curDataPath = filepath.Join(homeDir, ".kanivet", "cur-data")
	}

	if _, err := os.Stat(p.curDataPath); os.IsNotExist(err) {
		p.mu.Lock()
		p.lastError = fmt.Sprintf("CUR data path does not exist: %s", p.curDataPath)
		p.mu.Unlock()
		log.Printf("CUR data path does not exist: %s", p.curDataPath)
		return
	}

	files, err := filepath.Glob(filepath.Join(p.curDataPath, "*.csv"))
	if err != nil || len(files) == 0 {
		p.mu.Lock()
		p.lastError = fmt.Sprintf("No CUR CSV files found in %s", p.curDataPath)
		p.mu.Unlock()
		log.Printf("No CUR CSV files found in %s", p.curDataPath)
		return
	}

	for _, file := range files {
		p.parseCURFile(file)
	}

	p.mu.Lock()
	p.lastLoaded = time.Now()
	if len(p.priceCache) > 0 {
		p.lastError = ""
	}
	p.mu.Unlock()

	log.Printf("Loaded CUR pricing data: %d instance types", len(p.priceCache))
}

func (p *CURPricingProvider) parseCURFile(filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("Failed to open CUR file %s: %v", filePath, err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		log.Printf("Failed to read CUR headers: %v", err)
		return
	}

	colIdx := make(map[string]int)
	for i, h := range headers {
		colIdx[h] = i
	}

	requiredCols := []string{
		"lineItem/UsageType",
		"lineItem/UnblendedCost",
		"product/instanceType",
		"product/region",
		"product/vcpu",
		"product/memory",
	}
	for _, col := range requiredCols {
		if _, ok := colIdx[col]; !ok {
			altCol := strings.ReplaceAll(col, "/", "_")
			if _, ok := colIdx[altCol]; ok {
				colIdx[col] = colIdx[altCol]
			}
		}
	}

	instanceCosts := make(map[string]struct {
		totalCost  float64
		totalHours float64
		vcpu       int
		memGB      float64
		region     string
	})

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		usageType := p.getColumn(record, colIdx, "lineItem/UsageType")
		if !strings.Contains(usageType, "BoxUsage") {
			continue
		}

		instanceType := p.getColumn(record, colIdx, "product/instanceType")
		if instanceType == "" {
			continue
		}

		region := p.getColumn(record, colIdx, "product/region")
		cost, _ := strconv.ParseFloat(p.getColumn(record, colIdx, "lineItem/UnblendedCost"), 64)
		vcpu, _ := strconv.Atoi(p.getColumn(record, colIdx, "product/vcpu"))
		memStr := strings.TrimSuffix(p.getColumn(record, colIdx, "product/memory"), " GiB")
		memGB, _ := strconv.ParseFloat(memStr, 64)

		key := fmt.Sprintf("%s:%s", region, instanceType)
		data := instanceCosts[key]
		data.totalCost += cost
		data.totalHours += 1
		if vcpu > 0 {
			data.vcpu = vcpu
		}
		if memGB > 0 {
			data.memGB = memGB
		}
		data.region = region
		instanceCosts[key] = data
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for key, data := range instanceCosts {
		if data.totalHours == 0 {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		region, instanceType := parts[0], parts[1]
		hourlyPrice := data.totalCost / data.totalHours
		cpuRate, memRate := calculateResourceRates(hourlyPrice, data.vcpu, data.memGB)

		p.priceCache[key] = &InstancePricing{
			InstanceType:     instanceType,
			Region:           region,
			OnDemandPrice:    hourlyPrice,
			CPUCores:         data.vcpu,
			MemoryGB:         data.memGB,
			CPUHourlyRate:    cpuRate,
			MemoryHourlyRate: memRate,
			LastUpdated:      time.Now(),
		}
	}
}

func (p *CURPricingProvider) getColumn(record []string, colIdx map[string]int, colName string) string {
	if idx, ok := colIdx[colName]; ok && idx < len(record) {
		return record[idx]
	}
	return ""
}

func (p *CURPricingProvider) GetInstancePricing(instanceType, region string) (*InstancePricing, error) {
	p.mu.RLock()
	key := fmt.Sprintf("%s:%s", region, instanceType)
	if pricing, ok := p.priceCache[key]; ok {
		p.mu.RUnlock()
		return pricing, nil
	}
	p.mu.RUnlock()
	return nil, fmt.Errorf("no CUR data for %s in %s", instanceType, region)
}

func (p *CURPricingProvider) GetSpotPricing(instanceType, region, az string) (float64, error) {
	return 0, fmt.Errorf("spot pricing not available in CUR data")
}

func (p *CURPricingProvider) RefreshPricing() error {
	p.loadCURData(context.Background())
	return nil
}

func (p *CURPricingProvider) HasData() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.priceCache) > 0
}

type AzurePricingProvider struct {
	mu          sync.RWMutex
	priceCache  map[string]*InstancePricing
	lastRefresh time.Time
	lastError   string
	cacheDir    string
}

type azureRetailPrice struct {
	ArmRegionName        string  `json:"armRegionName"`
	ArmSkuName           string  `json:"armSkuName"`
	ServiceName          string  `json:"serviceName"`
	ProductName          string  `json:"productName"`
	MeterName            string  `json:"meterName"`
	RetailPrice          float64 `json:"retailPrice"`
	UnitPrice            float64 `json:"unitPrice"`
	UnitOfMeasure        string  `json:"unitOfMeasure"`
	Type                 string  `json:"type"`
	IsPrimaryMeterRegion bool    `json:"isPrimaryMeterRegion"`
}

type azurePriceResponse struct {
	Items        []azureRetailPrice `json:"Items"`
	NextPageLink string             `json:"NextPageLink"`
}

func NewAzurePricingProvider() *AzurePricingProvider {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".kanivet", "pricing-cache")
	os.MkdirAll(cacheDir, 0755)
	p := &AzurePricingProvider{
		priceCache: make(map[string]*InstancePricing),
		cacheDir:   cacheDir,
	}
	p.loadCacheFromDisk()
	return p
}

func (p *AzurePricingProvider) loadCacheFromDisk() {
	filePath := filepath.Join(p.cacheDir, "azure-pricing.json.gz")
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return
	}
	defer gz.Close()

	var cache diskPricingCache
	cacheData, err := io.ReadAll(gz)
	if err != nil {
		return
	}
	if err := jsonv2.Unmarshal(cacheData, &cache); err != nil {
		return
	}

	if time.Since(cache.SavedAt) > 7*24*time.Hour {
		os.Remove(filePath)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for key, pricing := range cache.Pricing {
		p.priceCache[key] = pricing
	}
	p.lastRefresh = cache.SavedAt
	if len(p.priceCache) > 0 {
		log.Printf("[Azure Pricing] Loaded %d cached instance types from disk", len(p.priceCache))
	}
}

func (p *AzurePricingProvider) saveCacheToDisk() {
	p.mu.RLock()
	pricingCopy := make(map[string]*InstancePricing)
	for key, pricing := range p.priceCache {
		pricingCopy[key] = pricing
	}
	p.mu.RUnlock()

	if len(pricingCopy) == 0 {
		return
	}

	cache := diskPricingCache{
		Region:  "azure",
		SavedAt: time.Now(),
		Pricing: pricingCopy,
	}

	filePath := filepath.Join(p.cacheDir, "azure-pricing.json.gz")
	file, err := os.Create(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()

	data, _ := jsonv2.Marshal(cache)
	gz.Write(data)
}

func (p *AzurePricingProvider) GetStatus() PricingStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return PricingStatus{
		Source:        PricingSourceAzureAPI,
		LastUpdated:   p.lastRefresh,
		InstanceCount: len(p.priceCache),
		IsAvailable:   len(p.priceCache) > 0 || p.lastRefresh.IsZero(),
		Error:         p.lastError,
	}
}

func (p *AzurePricingProvider) GetInstancePricing(instanceType, region string) (*InstancePricing, error) {
	p.mu.RLock()
	key := fmt.Sprintf("%s:%s", region, instanceType)
	if pricing, ok := p.priceCache[key]; ok {
		p.mu.RUnlock()
		return pricing, nil
	}
	p.mu.RUnlock()

	if err := p.loadInstancePricing(instanceType, region); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if pricing, ok := p.priceCache[key]; ok {
		return pricing, nil
	}

	return nil, fmt.Errorf("pricing not found for %s in %s", instanceType, region)
}

func (p *AzurePricingProvider) loadInstancePricing(instanceType, region string) error {
	armRegion := getAzureARMRegion(region)
	url := fmt.Sprintf(
		"https://prices.azure.com/api/retail/prices?api-version=2023-01-01-preview&$filter=serviceName eq 'Virtual Machines' and armRegionName eq '%s' and armSkuName eq '%s'",
		armRegion, instanceType,
	)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		p.mu.Lock()
		p.lastError = err.Error()
		p.mu.Unlock()
		return fmt.Errorf("failed to fetch Azure pricing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("Azure pricing API returned status %d", resp.StatusCode)
		p.mu.Lock()
		p.lastError = errMsg
		p.mu.Unlock()
		return errors.New(errMsg)
	}

	var priceResp azurePriceResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.mu.Lock()
		p.lastError = err.Error()
		p.mu.Unlock()
		return fmt.Errorf("failed to read Azure pricing response: %w", err)
	}
	if err := jsonv2.Unmarshal(body, &priceResp); err != nil {
		p.mu.Lock()
		p.lastError = err.Error()
		p.mu.Unlock()
		return fmt.Errorf("failed to parse Azure pricing: %w", err)
	}

	if len(priceResp.Items) == 0 {
		return fmt.Errorf("no pricing data found for %s in %s", instanceType, region)
	}

	var hourlyCost float64
	var vcpu int
	var memGB float64

	for _, item := range priceResp.Items {
		if item.UnitOfMeasure == "1 Hour" && item.Type == "Consumption" && item.IsPrimaryMeterRegion {
			hourlyCost = item.RetailPrice
			vcpu, memGB = p.extractSpecsFromProductName(item.ProductName)
			break
		}
	}

	if hourlyCost == 0 {
		for _, item := range priceResp.Items {
			if item.UnitOfMeasure == "1 Hour" && strings.Contains(strings.ToLower(item.MeterName), "compute") {
				hourlyCost = item.RetailPrice
				vcpu, memGB = p.extractSpecsFromProductName(item.ProductName)
				break
			}
		}
	}

	if hourlyCost == 0 {
		return fmt.Errorf("no hourly pricing found for %s in %s", instanceType, region)
	}

	if vcpu == 0 {
		vcpu, memGB = p.fallbackSpecs(instanceType)
		log.Printf("[Azure] Using fallback specs for %s: %d vCPU, %.1f GB", instanceType, vcpu, memGB)
	}

	pricing := &InstancePricing{
		InstanceType:     instanceType,
		Region:           region,
		OnDemandPrice:    hourlyCost,
		CPUCores:         vcpu,
		MemoryGB:         memGB,
		CPUHourlyRate:    hourlyCost * 0.7 / float64(vcpu),
		MemoryHourlyRate: hourlyCost * 0.3 / memGB,
		LastUpdated:      time.Now(),
	}

	p.mu.Lock()
	key := fmt.Sprintf("%s:%s", region, instanceType)
	p.priceCache[key] = pricing
	p.lastRefresh = time.Now()
	p.lastError = ""
	p.mu.Unlock()

	log.Printf("[Azure] Loaded pricing for %s in %s: $%.4f/hr (%d vCPU, %.1f GB)",
		instanceType, region, hourlyCost, vcpu, memGB)
	go p.saveCacheToDisk()
	return nil
}

func (p *AzurePricingProvider) extractSpecsFromProductName(productName string) (int, float64) {
	productLower := strings.ToLower(productName)

	var vcpu int
	var memGB float64

	if strings.Contains(productLower, " vcpu") {
		parts := strings.Fields(productName)
		for i, part := range parts {
			if strings.Contains(strings.ToLower(part), "vcpu") && i > 0 {
				if cpu, err := strconv.Atoi(parts[i-1]); err == nil {
					vcpu = cpu
					break
				}
			}
		}
	}

	if strings.Contains(productLower, " gb") || strings.Contains(productLower, "gb ram") {
		parts := strings.Fields(productName)
		for i, part := range parts {
			if strings.ToLower(part) == "gb" && i > 0 {
				if mem, err := strconv.ParseFloat(parts[i-1], 64); err == nil {
					memGB = mem
					break
				}
			}
		}
	}

	return vcpu, memGB
}

func (p *AzurePricingProvider) fallbackSpecs(instanceType string) (int, float64) {
	parts := strings.Split(instanceType, "_")
	if len(parts) < 2 {
		return 2, 8.0
	}

	size := parts[1]
	numStr := ""
	for _, c := range size {
		if c >= '0' && c <= '9' {
			numStr += string(c)
		}
	}

	num := 2
	if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
		num = n
	}

	series := strings.ToUpper(string(size[0]))
	switch series {
	case "B":
		return num, float64(num) * 4
	case "D":
		return num, float64(num) * 4
	case "E":
		return num, float64(num) * 8
	case "F":
		return num, float64(num) * 2
	default:
		return num, float64(num) * 4
	}
}

func (p *AzurePricingProvider) GetSpotPricing(instanceType, region, az string) (float64, error) {
	return 0, fmt.Errorf("Azure spot pricing not implemented")
}

func (p *AzurePricingProvider) RefreshPricing() error {
	return nil
}

func getAzureARMRegion(k8sRegion string) string {
	regionMap := map[string]string{
		"eastus":        "eastus",
		"eastus2":       "eastus2",
		"westus":        "westus",
		"westus2":       "westus2",
		"westus3":       "westus3",
		"centralus":     "centralus",
		"northeurope":   "northeurope",
		"westeurope":    "westeurope",
		"uksouth":       "uksouth",
		"ukwest":        "ukwest",
		"southeastasia": "southeastasia",
		"eastasia":      "eastasia",
		"japaneast":     "japaneast",
		"japanwest":     "japanwest",
		"australiaeast": "australiaeast",
		"canadacentral": "canadacentral",
		"brazilsouth":   "brazilsouth",
		"southindia":    "southindia",
		"centralindia":  "centralindia",
	}
	if armRegion, ok := regionMap[k8sRegion]; ok {
		return armRegion
	}
	return k8sRegion
}

type HybridPricingProvider struct {
	curProvider   *CURPricingProvider
	awsProvider   *AWSPricingProvider
	azureProvider *AzurePricingProvider
	mu            sync.RWMutex
	activeSource  PricingSource
	provider      string
}

func NewHybridPricingProvider(curConfig *CURConfig) *HybridPricingProvider {
	h := &HybridPricingProvider{
		awsProvider:   NewAWSPricingProvider(),
		azureProvider: NewAzurePricingProvider(),
		activeSource:  PricingSourceUnknown,
	}
	if curConfig != nil && curConfig.DataPath != "" {
		h.curProvider = NewCURPricingProvider(*curConfig)
	}
	return h
}

func (h *HybridPricingProvider) GetStatus() PricingStatus {
	h.mu.RLock()
	provider := h.provider
	h.mu.RUnlock()

	if h.curProvider != nil && h.curProvider.HasData() {
		status := h.curProvider.GetStatus()
		status.Source = PricingSourceCUR
		return status
	}

	if strings.Contains(provider, "Azure") || strings.Contains(provider, "AKS") {
		return h.azureProvider.GetStatus()
	}

	return h.awsProvider.GetStatus()
}

func (h *HybridPricingProvider) SetProvider(provider string) {
	h.mu.Lock()
	h.provider = provider
	h.mu.Unlock()
}

func (h *HybridPricingProvider) GetInstancePricing(instanceType, region string) (*InstancePricing, error) {
	if h.curProvider != nil && h.curProvider.HasData() {
		if pricing, err := h.curProvider.GetInstancePricing(instanceType, region); err == nil {
			h.mu.Lock()
			h.activeSource = PricingSourceCUR
			h.mu.Unlock()
			return pricing, nil
		}
	}

	h.mu.RLock()
	provider := h.provider
	h.mu.RUnlock()

	if strings.Contains(provider, "Azure") || strings.Contains(provider, "AKS") {
		pricing, err := h.azureProvider.GetInstancePricing(instanceType, region)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.activeSource = PricingSourceAzureAPI
		h.mu.Unlock()
		return pricing, nil
	}

	pricing, err := h.awsProvider.GetInstancePricing(instanceType, region)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.activeSource = PricingSourceAWSAPI
	h.mu.Unlock()

	return pricing, nil
}

func (h *HybridPricingProvider) GetSpotPricing(instanceType, region, az string) (float64, error) {
	return h.awsProvider.GetSpotPricing(instanceType, region, az)
}

func (h *HybridPricingProvider) RefreshPricing() error {
	if h.curProvider != nil {
		_ = h.curProvider.RefreshPricing()
	}
	return h.awsProvider.RefreshPricing()
}

// GetInstancePricingCached is GetInstancePricing minus the blocking AWS
// offer-file download: cold regions load in the background instead.
func (h *HybridPricingProvider) GetInstancePricingCached(instanceType, region string) (*InstancePricing, error) {
	if h.curProvider != nil && h.curProvider.HasData() {
		if pricing, err := h.curProvider.GetInstancePricing(instanceType, region); err == nil {
			h.mu.Lock()
			h.activeSource = PricingSourceCUR
			h.mu.Unlock()
			return pricing, nil
		}
	}

	h.mu.RLock()
	provider := h.provider
	h.mu.RUnlock()

	if strings.Contains(provider, "Azure") || strings.Contains(provider, "AKS") {
		pricing, err := h.azureProvider.GetInstancePricing(instanceType, region)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.activeSource = PricingSourceAzureAPI
		h.mu.Unlock()
		return pricing, nil
	}

	pricing, err := h.awsProvider.GetInstancePricingCached(instanceType, region)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.activeSource = PricingSourceAWSAPI
	h.mu.Unlock()

	return pricing, nil
}

// EnsureRegions returns true when pricing for every region is already loaded
// (stale is fine — the background refresh handles it). When cold it starts
// the multi-hundred-MB offer download in the background, fires onLoaded when
// it lands, and returns false so callers render without prices instead of
// blocking the dashboard for minutes on slow links.
func (h *HybridPricingProvider) EnsureRegions(regions []string, onLoaded func()) bool {
	h.mu.RLock()
	provider := h.provider
	h.mu.RUnlock()

	if strings.Contains(provider, "Azure") || strings.Contains(provider, "AKS") {
		return true
	}
	if h.awsProvider.RegionsLoaded(regions) {
		go h.PreloadRegions(regions)
		return true
	}
	go func() {
		h.PreloadRegions(regions)
		if onLoaded != nil {
			onLoaded()
		}
	}()
	return false
}

func (h *HybridPricingProvider) PreloadRegions(regions []string) {
	h.mu.RLock()
	provider := h.provider
	h.mu.RUnlock()

	if strings.Contains(provider, "Azure") || strings.Contains(provider, "AKS") {
		return
	}

	var wg sync.WaitGroup
	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			if err := h.awsProvider.ensureRegionLoaded(r); err != nil {
				log.Printf("Failed to preload pricing for region %s: %v", r, err)
			}
		}(region)
	}
	wg.Wait()
}
