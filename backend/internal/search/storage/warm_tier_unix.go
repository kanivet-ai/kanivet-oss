//go:build !windows

package storage

import (
	"os"
	"path/filepath"
	"syscall"
)

type platformData struct{}

func NewWarmTier(dataDir string, pools *InternPools) (*WarmTier, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "warm_tier.dat")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	size := info.Size()
	if size < warmTierInitialSize {
		if err := file.Truncate(warmTierInitialSize); err != nil {
			file.Close()
			return nil, err
		}
		size = warmTierInitialSize
	}

	data, err := syscall.Mmap(int(file.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		file.Close()
		return nil, err
	}

	return &WarmTier{
		file:       file,
		data:       data,
		docOffsets: make(map[uint32]uint32, 10000),
		writePos:   0,
		bloom:      make([]uint64, bloomFilterSize/64),
		pools:      pools,
	}, nil
}

func (w *WarmTier) grow() error {
	if err := syscall.Munmap(w.data); err != nil {
		return err
	}

	newSize := int64(len(w.data)) + warmTierGrowthSize
	if err := w.file.Truncate(newSize); err != nil {
		return err
	}

	data, err := syscall.Mmap(int(w.file.Fd()), 0, int(newSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}

	w.data = data
	return nil
}

func (w *WarmTier) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if w.data != nil {
		syscall.Munmap(w.data)
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
