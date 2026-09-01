package storage

import (
	"encoding/binary"
	"os"
	"sync"
)

const (
	warmTierInitialSize = 1024 * 1024 * 10
	warmTierGrowthSize  = 1024 * 1024 * 10
	bloomFilterSize     = 1 << 20
	bloomFilterHashes   = 4
	recordSize          = 36
)

type WarmTier struct {
	mu         sync.RWMutex
	file       *os.File
	data       []byte
	docOffsets map[uint32]uint32
	writePos   uint32
	bloom      []uint64
	pools      *InternPools
	closed     bool
	platform   platformData
}

func (w *WarmTier) bloomSet(docID uint32) {
	for i := uint32(0); i < bloomFilterHashes; i++ {
		h := hash(docID, i) % bloomFilterSize
		w.bloom[h/64] |= 1 << (h % 64)
	}
}

func (w *WarmTier) bloomMayExist(docID uint32) bool {
	for i := uint32(0); i < bloomFilterHashes; i++ {
		h := hash(docID, i) % bloomFilterSize
		if w.bloom[h/64]&(1<<(h%64)) == 0 {
			return false
		}
	}
	return true
}

func hash(v, seed uint32) uint32 {
	v ^= seed * 0x9e3779b9
	v ^= v >> 16
	v *= 0x85ebca6b
	v ^= v >> 13
	v *= 0xc2b2ae35
	v ^= v >> 16
	return v
}

func (w *WarmTier) Store(compact CompactResource) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	if w.writePos+recordSize > uint32(len(w.data)) {
		if err := w.grow(); err != nil {
			return err
		}
	}

	offset := w.writePos
	binary.LittleEndian.PutUint32(w.data[offset:], compact.ID)
	binary.LittleEndian.PutUint32(w.data[offset+4:], compact.Cluster)
	binary.LittleEndian.PutUint32(w.data[offset+8:], compact.Kind)
	binary.LittleEndian.PutUint32(w.data[offset+12:], compact.Namespace)
	binary.LittleEndian.PutUint32(w.data[offset+16:], compact.Name)
	binary.LittleEndian.PutUint32(w.data[offset+20:], compact.Group)
	binary.LittleEndian.PutUint32(w.data[offset+24:], compact.Version)
	w.data[offset+28] = byte(compact.Category)
	binary.LittleEndian.PutUint64(w.data[offset+29:], uint64(compact.UpdatedAt))

	w.docOffsets[compact.ID] = offset
	w.bloomSet(compact.ID)
	w.writePos += recordSize

	return nil
}

func (w *WarmTier) Get(docID uint32) (CompactResource, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.closed || !w.bloomMayExist(docID) {
		return CompactResource{}, false
	}

	offset, ok := w.docOffsets[docID]
	if !ok {
		return CompactResource{}, false
	}

	if offset+recordSize > uint32(len(w.data)) {
		return CompactResource{}, false
	}

	return CompactResource{
		ID:        binary.LittleEndian.Uint32(w.data[offset:]),
		Cluster:   binary.LittleEndian.Uint32(w.data[offset+4:]),
		Kind:      binary.LittleEndian.Uint32(w.data[offset+8:]),
		Namespace: binary.LittleEndian.Uint32(w.data[offset+12:]),
		Name:      binary.LittleEndian.Uint32(w.data[offset+16:]),
		Group:     binary.LittleEndian.Uint32(w.data[offset+20:]),
		Version:   binary.LittleEndian.Uint32(w.data[offset+24:]),
		Category:  CategoryType(w.data[offset+28]),
		UpdatedAt: int64(binary.LittleEndian.Uint64(w.data[offset+29:])),
	}, true
}

func (w *WarmTier) Remove(docID uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.docOffsets, docID)
}

func (w *WarmTier) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.docOffsets)
}

func (w *WarmTier) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.docOffsets = make(map[uint32]uint32, 10000)
	w.bloom = make([]uint64, bloomFilterSize/64)
	w.writePos = 0
}
