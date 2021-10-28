// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creachadair/atomicfile"
	"github.com/creachadair/tarsnap"
)

// ListCache contains the data stored in the persistent archive list cache.
type ListCache struct {
	Tag      string           `json:"cacheTag"`
	Archives tarsnap.Archives `json:"archiveList"`
}

// LoadFrom populates c from the data stored in the specified file.
func (c *ListCache) LoadFrom(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, c)
}

// SaveTo updates the specified file with the current list cache data.
func (c *ListCache) SaveTo(path string) error {
	bits, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding cache listing: %v", err)
	} else if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating list cache directory: %v", err)
	} else if err := atomicfile.WriteData(path, bits, 0600); err != nil {
		return fmt.Errorf("writing cache file: %v", err)
	}
	return nil
}
