// Program snapback creates tarsnap backups of important directories.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"bitbucket.org/creachadair/shell"
	"bitbucket.org/creachadair/tarsnap"
)

// TODO: Pull out the config.

// TODO: Prune old backups by age.
// - Keep everything from the last day.
// - Keep 1/day for up to 30 days.
// - Keep 1/week for up to 3 months.
// - Keep 1/month for up to 12 months.
// - Keep 1/year after that.

var config = map[string]tarsnap.CreateOptions{
	"documents": {Include: []string{"Documents", "Desktop", "Downloads"}},
	"blobdata":  {Include: []string{"data"}},
	"pictures":  {Include: []string{"Pictures"}},
	"software":  {Include: []string{"software"}},
	"dotfiles": {
		Include: []string{".dotfiles"},
		Modify:  []string{`/^\.//`},
	},
	"library": {
		Include: []string{
			"Library/Application Support",
			"Library/Accounts",
			"Library/Calendars",
			"Library/Keychains",
			"Library/Mail",
			"Library/Preferences",
		},
	},
}

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: %[1]s [-v] -list  # list existing backups
       %[1]s [-v]        # create new backups

Create tarsnap backups of important directories. With the -v flag, the
underlying tarsnap commands will be logged to stderr.

Options:
`, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
}

var (
	workDir   = flag.String("dir", os.Getenv("HOME"), "Run from this directory")
	doList    = flag.Bool("list", false, "List known archives")
	doDryRun  = flag.Bool("dry-run", false, "Simulate creating archives")
	doVerbose = flag.Bool("v", false, "Verbose logging")
)

func main() {
	flag.Parse()

	cfg := &tarsnap.Config{Dir: *workDir}
	if *doVerbose {
		cfg.CmdLog = func(cmd string, args []string) {
			fmt.Fprintf(os.Stderr, "+ [%s] %s\n", cmd, shell.Join(args))
		}
	}

	if *doList {
		as, err := cfg.Archives()
		if err != nil {
			log.Fatalf("Listing archives: %v", err)
		}
		for _, arch := range as {
			fmt.Printf("%s\t%s\n", arch.Created.Format(time.RFC3339), arch.Name)
		}
		return
	}
	start := time.Now()
	if err := createBackups(cfg, config); err != nil {
		log.Fatalf("Failed: %v", err)
	}
	log.Printf("Backups finished [%v elapsed]", time.Since(start).Round(time.Second))
}

func createBackups(cfg *tarsnap.Config, config map[string]tarsnap.CreateOptions) error {
	tag := "." + time.Now().Format("20060102-1504")
	nerrs := 0
	for base, opts := range config {
		if *doDryRun {
			opts.DryRun = true
		}
		name := base + tag
		err := cfg.Create(name, opts)
		if err != nil {
			log.Printf("ERROR: %s: %v", name, err)
			nerrs++
		}
	}
	if nerrs > 0 {
		return fmt.Errorf("%d errors", nerrs)
	}
	return nil
}
