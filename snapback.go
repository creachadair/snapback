// Program snapback creates tarsnap backups of important directories.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"bitbucket.org/creachadair/shell"
	"bitbucket.org/creachadair/snapback/config"
	"bitbucket.org/creachadair/tarsnap"
)

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
	doPrune    = flag.Bool("prune", false, "Prune out-of-band archives")
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

	if *doList || *doPrune {
		if *doList && *doPrune {
			log.Fatal("The -list and -prune options are mutually exclusive")
		}
		arch, err := ts.Archives()
		if err != nil {
			log.Fatalf("Listing archives: %v", err)
		}
		if *doList {
			listArchives(arch)
			return
		}
		var prune []string
		for _, p := range cfg.FindExpired(arch) {
			prune = append(prune, p.Name)
		}
		if len(prune) == 0 {
			fmt.Fprintln(os.Stderr, "Nothing to prune")
		} else if *doDryRun {
			fmt.Fprintln(os.Stderr, "-- Pruning would remove these archives:")
			fmt.Println(strings.Join(prune, "\n"))
		} else if err := ts.Delete(prune...); err != nil {
			log.Fatalf("Deleting archives: %v", err)
		}
		return
	}
	if *doSize {
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

func listArchives(as []tarsnap.Archive) {
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
	fmt.Fprintf(tw, "TOTAL\t%s\t%s\t%s\t%s\n", H(info.All.InputBytes), H(info.All.CompressedBytes),
		H(info.All.UniqueBytes), H(info.All.CompressedUniqueBytes))
	for arch, size := range info.Archive {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", arch, H(size.InputBytes), H(size.CompressedBytes),
			H(size.UniqueBytes), H(size.CompressedUniqueBytes))
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

// H converts z into a human-readable string.
func H(z int64) string {
	switch {
	case z < 1<<10:
		return strconv.FormatInt(z, 10)
	case z <= 1<<20:
		return fmt.Sprintf("%.1fK", float64(z)/(1<<10))
	case z <= 1<<30:
		return fmt.Sprintf("%.1fM", float64(z)/(1<<20))
	case z <= 1<<40:
		return fmt.Sprintf("%.1fG", float64(z)/(1<<30))
	default:
		return fmt.Sprintf("%.1fT", float64(z)/(1<<40))
	}
}
