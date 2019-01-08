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
	"text/template"
	"time"

	"bitbucket.org/creachadair/shell"
	"bitbucket.org/creachadair/snapback/config"
	"bitbucket.org/creachadair/stringset"
	"bitbucket.org/creachadair/tarsnap"
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: %[1]s [-v] -list  # list existing backups
       %[1]s [-v] -prune # clean up old backups
       %[1]s [-v] -size  # show sizes of stored data
       %[1]s [-v]        # create new backups

Create tarsnap backups of important directories. With the -v flag, the
underlying tarsnap commands will be logged to stderr. If -dry-run is true, no
archives are created or deleted.

With -list and -size, the non-flag arguments are used to select which archives
to list or evaluate. Globs are permitted in these arguments.

With -prune, archives filtered by expiration policies are deleted. Non-flag
arguments specify archive sets to evaluate for pruning. Archive ages are pruned
based on the current time. For testing, you may override this by setting the
SNAPBACK_TIME environment to a string of the form 2006-01-02T15:04:05.

Options:
`, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
}

var (
	configFile = flag.String("config", "$HOME/.snapback", "Configuration file")
	outFormat  = flag.String("format", "", "Output format (Go template)")
	doList     = flag.Bool("list", false, "List known archives")
	doPrune    = flag.Bool("prune", false, "Prune out-of-band archives")
	doSize     = flag.Bool("size", false, "Print size statistics")
	doDryRun   = flag.Bool("dry-run", false, "Simulate creating or deleting archives")
	doVerbose  = flag.Bool("v", false, "Verbose logging")
)

func main() {
	flag.Parse()

	dir, cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Loading configuration: %v", err)
	}
	if cfg.Verbose {
		*doVerbose = true
	}
	if *doPrune && *doDryRun {
		cfg.Verbose = true
	}
	ts := &cfg.Config
	ts.CmdLog = logCommand
	if ts.WorkDir == "" {
		ts.WorkDir = dir
	}

	// If we need a list of existing archives, grab it.
	var arch []tarsnap.Archive
	if *doList || *doPrune || (*doSize && hasGlob(flag.Args())) {
		arch, err = ts.Archives()
		if err != nil {
			log.Fatalf("Listing archives: %v", err)
		}
	}
	if *doList {
		listArchives(cfg, arch)
		return
	}
	if *doPrune {
		pruneArchives(cfg, arch)
		return
	}

	if *doSize {
		printSizes(cfg, arch)
		return
	} else if flag.NArg() != 0 {
		log.Fatalf("Extra arguments after command: %v", flag.Args())
	}

	start := time.Now()
	if err := createBackups(cfg); err != nil {
		log.Fatalf("Failed: %v", err)
	}
	log.Printf("Backups finished [%v elapsed]", time.Since(start).Round(time.Second))
}

func listArchives(_ *config.Config, as []tarsnap.Archive) {
	ft := *outFormat
	if ft == "" {
		ft = "{{.Created}}\t{{.Name}}\n"
	} else if !strings.HasSuffix(ft, "\n") {
		ft += "\n"
	}
	t, err := template.New("list").Parse(ft)
	if err != nil {
		log.Fatalf("Parsing output format: %v", err)
	}
	for _, arch := range as {
		if matchExpr(arch.Name, flag.Args()) {
			err := t.Execute(os.Stdout, struct {
				Created string
				tarsnap.Archive
			}{Created: arch.Created.In(time.Local).Format(time.RFC3339), Archive: arch})
			if err != nil {
				log.Fatalf("Writing %v: %v", arch, err)
			}
		}
	}
}

func pruneArchives(cfg *config.Config, as []tarsnap.Archive) {
	now := time.Now()
	if et, ok := os.LookupEnv("SNAPBACK_TIME"); ok {
		t, err := time.Parse("2006-01-02T15:04:05", et)
		if err != nil {
			log.Fatalf("Parsing SNAPBACK_TIME %q: %v", et, err)
		}
		now = t
		fmt.Fprintf(os.Stderr, "Using current time from SNAPBACK_TIME: %v\n", now)
	}
	pick := func(tarsnap.Archive) bool { return true }
	if flag.NArg() != 0 {
		s := stringset.New(flag.Args()...)
		pick = func(a tarsnap.Archive) bool {
			return s.Contains(a.Base)
		}
	}

	var prune []string
	for _, p := range cfg.FindExpired(as, now) {
		if pick(p) {
			prune = append(prune, p.Name)
		}
	}
	if len(prune) == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to prune")
		return
	} else if *doDryRun {
		fmt.Fprintln(os.Stderr, "-- Pruning would remove these archives:")
	} else if err := cfg.Config.Delete(prune...); err != nil {
		log.Fatalf("Deleting archives: %v", err)
	}
	fmt.Println(strings.Join(prune, "\n"))
}

func printSizes(cfg *config.Config, as []tarsnap.Archive) {
	var names []string
	if as == nil {
		names = flag.Args()
	} else {
		for _, a := range as {
			if matchExpr(a.Name, flag.Args()) {
				names = append(names, a.Name)
			}
		}
	}

	info, err := cfg.Config.Size(names...)
	if err != nil {
		log.Fatalf("Reading stats: %v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 1, ' ', 0)
	fmt.Fprintf(tw, "TOTAL\t%s raw\t%s comp\t%s uniq\t%s incr\n",
		H(info.All.InputBytes), H(info.All.CompressedBytes),
		H(info.All.UniqueBytes), H(info.All.CompressedUniqueBytes))
	for _, arch := range as {
		size, ok := info.Archive[arch.Name]
		if ok {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", arch.Name,
				H(size.InputBytes), H(size.CompressedBytes),
				H(size.UniqueBytes), H(size.CompressedUniqueBytes))
		}
	}
	tw.Flush()
}

func createBackups(cfg *config.Config) error {
	tag := "." + time.Now().Format("20060102-1504")
	nerrs := 0
	for _, b := range cfg.Backup {
		opts := b.CreateOptions
		opts.DryRun = *doDryRun
		name := b.Name + tag
		if err := cfg.Config.Create(name, opts); err != nil {
			log.Printf("ERROR: %s: %v", name, err)
			nerrs++
		} else {
			fmt.Println(name)
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

func isGlob(s string) bool {
	qt := false
	for _, c := range s {
		switch c {
		case '\\':
			qt = !qt
		case '[', '*', '?':
			if !qt {
				return true
			}
			qt = false
		}
	}
	return false
}

func hasGlob(args []string) bool {
	for _, arg := range args {
		if isGlob(arg) {
			return true
		}
	}
	return false
}
