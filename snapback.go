// Program snapback creates tarsnap backups of important directories.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"bitbucket.org/creachadair/shell"
	"bitbucket.org/creachadair/snapback/config"
	"bitbucket.org/creachadair/tarsnap"
)

// TODO: Prune old backups by age.
// - Keep everything from the last day.
// - Keep 1/day for up to 30 days.
// - Keep 1/week for up to 3 months.
// - Keep 1/month for up to 12 months.
// - Keep 1/year after that.

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
	configFile = flag.String("config", "$HOME/.snapback", "Configuration file")
	doList     = flag.Bool("list", false, "List known archives")
	doSize     = flag.Bool("size", false, "Print size statistics")
	doDryRun   = flag.Bool("dry-run", false, "Simulate creating archives")
	doVerbose  = flag.Bool("v", false, "Verbose logging")
)

func main() {
	flag.Parse()

	dir, cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Loading configuration: %v", err)
	}
	ts := &cfg.Config
	ts.CmdLog = logCommand
	if ts.WorkDir == "" {
		ts.WorkDir = dir
	}

	if *doList {
		listArchives(ts)
		return
	} else if *doSize {
		printSizes(ts)
		return
	} else if flag.NArg() != 0 {
		log.Fatalf("Extra arguments after command: %v", flag.Args())
	}

	start := time.Now()
	if err := createBackups(ts, cfg); err != nil {
		log.Fatalf("Failed: %v", err)
	}
	log.Printf("Backups finished [%v elapsed]", time.Since(start).Round(time.Second))
}

func listArchives(ts *tarsnap.Config) {
	as, err := ts.Archives()
	if err != nil {
		log.Fatalf("Listing archives: %v", err)
	}
	for _, arch := range as {
		if matchExpr(arch.Name, flag.Args()) {
			fmt.Printf("%s\t%s\n", arch.Created.In(time.Local).Format(time.RFC3339), arch.Name)
		}
	}
}

func printSizes(ts *tarsnap.Config) {
	info, err := ts.Size(flag.Args()...)
	if err != nil {
		log.Fatalf("Reading stats: %v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 1, ' ', 0)
	fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\t%d\n", info.All.InputBytes, info.All.CompressedBytes,
		info.All.UniqueBytes, info.All.CompressedUniqueBytes)
	for arch, size := range info.Archive {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", arch, size.InputBytes, size.CompressedBytes,
			size.UniqueBytes, size.CompressedUniqueBytes)
	}
	tw.Flush()
}

func createBackups(ts *tarsnap.Config, cfg *config.Config) error {
	tag := "." + time.Now().Format("20060102-1504")
	nerrs := 0
	for _, b := range cfg.Backup {
		opts := b.CreateOptions
		opts.DryRun = *doDryRun
		name := b.Name + tag
		if err := ts.Create(name, opts); err != nil {
			log.Printf("ERROR: %s: %v", name, err)
			nerrs++
		}
	}
	if nerrs > 0 {
		return fmt.Errorf("%d errors", nerrs)
	}
	return nil
}

func matchExpr(name string, exprs []string) bool {
	for _, expr := range exprs {
		ok, err := filepath.Match(expr, name)
		if ok && err == nil {
			return true
		}
	}
	return len(exprs) == 0
}

func loadConfig(path string) (string, *config.Config, error) {
	loc, err := filepath.Abs(os.ExpandEnv(path))
	if err != nil {
		return "", nil, err
	}
	f, err := os.Open(loc)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	cfg, err := config.Parse(f)
	return filepath.Dir(loc), cfg, err
}

func logCommand(cmd string, args []string) {
	if *doVerbose {
		fmt.Fprintf(os.Stderr, "+ [%s] %s\n", cmd, shell.Join(args))
	}
}
