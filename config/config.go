// Package config describes configuration settings for the snapback tool.
package config

import (
	"io"
	"os"

	"bitbucket.org/creachadair/tarsnap"
	yaml "gopkg.in/yaml.v2"
)

// A Config contains settings for the snapback tool.
type Config struct {
	// An ordered list of backups to be created.
	Backup []*Backup

	// Configuration settings for the tarsnap tool.
	tarsnap.Config `yaml:",inline"`
}

// TODO: Add pruning policy.

// A Backup describes a collection of files to be backed up as a unit together.
type Backup struct {
	// The name defines the base name of the archive. A timestamp will be
	// appended to this name to obtain the complete name.
	Name string

	// The archive creation options for this backup.
	tarsnap.CreateOptions `yaml:",inline"`
}

// Parse decodes a *Config from the specified reader.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.SetStrict(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	expand(&cfg.Keyfile)
	expand(&cfg.WorkDir)
	for _, b := range cfg.Backup {
		expand(&b.WorkDir)
	}
	return &cfg, nil
}

func expand(s *string) { *s = os.ExpandEnv(*s) }
