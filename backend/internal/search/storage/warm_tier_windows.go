//go:build windows

package storage

import (
	"io"
	"os"
	"path/filepath"
)

type platformData struct {
	size int64
}

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

	data := make([]byte, size)
	if _, err := file.ReadAt(data, 0); err != nil && err != io.EOF {
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
		platform:   platformData{size: size},
	}, nil
}

func (w *WarmTier) grow() error {
	if _, err := w.file.WriteAt(w.data[:w.writePos], 0); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}

	newSize := int64(len(w.data)) + warmTierGrowthSize
	if err := w.file.Truncate(newSize); err != nil {
		return err
	}

	newData := make([]byte, newSize)
	copy(newData, w.data)
	w.data = newData
	w.platform.size = newSize
	return nil
}

func (w *WarmTier) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if w.file != nil && w.data != nil {
		if _, err := w.file.WriteAt(w.data[:w.writePos], 0); err != nil {
			w.file.Close()
			return err
		}
		return w.file.Close()
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
