package storage

import "time"

type CategoryType uint8

const (
	CategoryOther CategoryType = iota
	CategoryWorkloads
	CategoryNetworking
	CategoryConfiguration
	CategoryStorage
	CategoryCluster
	CategorySecurity
	CategoryAutoscaling
	CategoryPolicy
	CategoryKind
)

var categoryStrings = []string{
	"Other", "Workloads", "Networking", "Configuration", "Storage",
	"Cluster", "Security", "Autoscaling", "Policy", "Kind",
}

func (c CategoryType) String() string {
	if int(c) < len(categoryStrings) {
		return categoryStrings[c]
	}
	return "Other"
}

func CategoryFromString(s string) CategoryType {
	switch s {
	case "Workloads":
		return CategoryWorkloads
	case "Networking":
		return CategoryNetworking
	case "Configuration":
		return CategoryConfiguration
	case "Storage":
		return CategoryStorage
	case "Cluster":
		return CategoryCluster
	case "Security":
		return CategorySecurity
	case "Autoscaling":
		return CategoryAutoscaling
	case "Policy":
		return CategoryPolicy
	case "Kind":
		return CategoryKind
	default:
		return CategoryOther
	}
}

type CompactResource struct {
	ID        uint32
	Cluster   uint32
	Kind      uint32
	Namespace uint32
	Name      uint32
	Group     uint32
	Version   uint32
	Category  CategoryType
	UpdatedAt int64
}

func (c *CompactResource) ToSearchable(pools *InternPools) SearchableResource {
	return SearchableResource{
		ID:        pools.IDs.Get(c.ID),
		Cluster:   pools.Clusters.Get(c.Cluster),
		Kind:      pools.Kinds.Get(c.Kind),
		Namespace: pools.Namespaces.Get(c.Namespace),
		Name:      pools.Names.Get(c.Name),
		Group:     pools.Groups.Get(c.Group),
		Version:   pools.Versions.Get(c.Version),
		Category:  c.Category.String(),
		UpdatedAt: time.Unix(c.UpdatedAt, 0),
	}
}

func CompactFromSearchable(r SearchableResource, pools *InternPools) CompactResource {
	return CompactResource{
		ID:        pools.IDs.Intern(r.ID),
		Cluster:   pools.Clusters.Intern(r.Cluster),
		Kind:      pools.Kinds.Intern(r.Kind),
		Namespace: pools.Namespaces.Intern(r.Namespace),
		Name:      pools.Names.Intern(r.Name),
		Group:     pools.Groups.Intern(r.Group),
		Version:   pools.Versions.Intern(r.Version),
		Category:  CategoryFromString(r.Category),
		UpdatedAt: r.UpdatedAt.Unix(),
	}
}
