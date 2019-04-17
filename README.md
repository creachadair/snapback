# snapback: A tarsnap backup tool

`snapback` is a command-line wrapper tool that makes it easier to manage backups on the [tarsnap][ts] service. Under the covers, it calls out to the `tarsnap` command-line tool to create, list, and delete backups, and can read settings from a configuration file.


## Installation

To use `snapback`, you will need [tarsnap][tsdl] and [Go][godl].  Once you have these installed, run:

```shell
$ go get bitbucket.org/creachadair/snapback
```

If you get an error like `"cannot find main module"` prepend `env GO111MODULE=off`.

Provided you have `$GOPATH/bin` in your `$PATH` environment, you should be able to verify that things are working by running:

```shell
$ snapback -help
```

## Quick Reference

For a summary of options, run `snapback -help`. For each of the commands shown here, you can also add `-dry-run` to prevent the tool from making any changes. The `tarsnap` tool has built-in support for `--dry-run` when creating new archives, and `snapback` adds support for a dry run on `-prune` as well.

-  Create backups: `snapback`

-  List the archives known to exist: `snapback -list`

    * To list archives matching a pattern: `snapback -list basename.*`

-  Show the size of all stored data: `snapback -size`

	* Show the size of a specific archive: `snapback -size archivename`
	* Show the sizes of matching archives: `snapback -size *.201812??-*`

-  Prune old archives: `snapback -prune`

## Configuration

The default configuration file is `$HOME/.snapback`, or you can use the `-config` flag to pick a different file. The file is in [YAML][yaml] format. The following example illustrates the available settings:

```yaml
# Example snapback configuration file.
# This example shows all the available settings, but in most cases the defaults
# should suffice apart from specifying backups (see below).

# Settings for tarsnap. You won't usually need to change these from the default
# unless you are using different settings for snapback runs.
tool: "path to tarsnap"      # default: uses $PATH
keyFile: "path to key file"  # default: uses tarsnap settings

# Where backups should be started from by default. The default is $HOME.
workDir: "directory path"

# Enable verbose logging output. The default is false.
verbose: true

# Default expiration settings. These settings govern how old backups are
# cleaned up by snapback -prune, and are used for every backup that does
# not provide its own expiration rules.
expiration:
- latest: 3       # keep the latest three archives of every set

- after: 1 day    # after one day, keep one archive per day
  sample: 1/day

- after: 1 month  # after one month, keep one archive per week
  sample: 1/week

- after: 6 months # after six months, keep one archive per month.
  sample: 1/month

# It is also possible to have named expiration policies. A backup can
# refer to such a policy by name (see below). If a backup has an explicit
# expiration policy, it supersedes any named policy. The name "default"
# is an alias for the default policy (see above).
policy:
  short:
  - latest: 2      # keep the latest two archives of every set
    sample: 1/day  # otherwise keep at most one per day
  - after: 2 weeks # after two weeks, discard everything
    sample: none

# Backups. Each backup in this list defines a collection of related backups,
# identified by a base name. Tarsnap requires unique names, so snapback appends
# a timestamp like ".20190315-1845" to generate an archive name. You may have
# as many backups as you like, but the names must not repeat.
backup:
- name: documents
  workDir: "path"  # change to this directory before archiving
  include:         # directories to include (recursively)
  - Documents
  - Desktop
  exclude:         # patterns to exclude from the backup
  - .git/**
  modify:
  - /^\\.//        # path modification rules (see "man tarsnap")

  followSymLinks: false   # follow (true) or store (false) symlinks
  storeAccessTime: false  # store (true) or omit (false) file access times
  preservePaths: false    # keep (true) or trim (false) absolute paths

  # Each backup may have its own expiration settings, which override
  # the default settings shown above.
  expiration:
  - latest: 10
  - after: 28 days
    sample: 1/week

- name: pictures
  include: [Pictures]
  expiration:
  - until: 10 days
    sample: all      # keep everything in this interval
  - after: 10 days
	sample: 1/month

- name: downloads
  include: [Downloads]
  policy: short      # use the "short" expiration policy

- name: programs
  policy: default    # use the default policy (explicitly)
  include: [/usr/local/bin]
```

### Expiration Policies

Running `snapback -prune` removes archives that have "expired" according to a
policy defined in the configuration file.

An expiration policy is a list of rules that specify which archives should be
kept or discarded based on their time of creation. Each rule specifies a span
of time, a number of unconditional snapshots, and a sampling rate. In a config
file this looks like:

```yaml
after:  <interval>
before: <interval>
latest: <count>
sample: <count>/<interval>  # or "all" or "none"
```

An `<interval>` is a string specifying a time interval, consisting of a number
and a unit, e.g., `1 week`, `2.5 days`, `3 months`. Fractions are allowed. For
purposes of this tool, a "day" is defined as 24 hours and a "year" as 365.25
days, and a "month" as 1/12 of a year or 30.4 days. The units understood are
seconds, hours, days, weeks, months, and years, and each of these may be
abbreviated to a single letter, e.g., `2w`, `15.2h`.

The `after` and `before` fields define a span of time before the present
moment, for example `after: 1 week` and `before: 1 month` covers the range of
time between 1 week and 1 month ago. An rule is _applicable_ to an archive if
the archive's creation time falls within the rule's span. If both fields are 0
(or not set), the rule spans all time.

The `latest` field specifies a number of most-recently created archives within
the rule span that should be retained unconditionally, regardless of age.

The `sample` field specifies a sampling rule that selects up to `<count>`
archives from each `<interval>` of time, evenly spaced throughout the rule's
span. If `sample` is not set, or is set to `none`, no samples are retained; if
`sample` is `all` every archive in the span is retained.

#### Rule Selection

Expiration is determined by evaluating each archive against the rules in the
policy. Among the applicable rules, the rule with the earliest, narrowest span
governs the archive's expiration. For example, suppose an archive X was created
7 days ago and we have these rules:

```yaml
# Rule 1
{after: 1 day, before: 10 days}

# Rule 2
{after: 4 days, before: 8 days}

# Rule 3
{after: 5 days, before: 9 days}

# Rule 4
{after: 3 days, before: 6 days}
```

Then X is governed by Rule 2. Rule 4 is inapplicable because it does not span
the creation time of X. Rules 2 and 3 are preferable to Rule 1 because their
spans are only 4 days, whereas Rule 1 spans 9 days. Rule 2 is preferable to
Rule 3 because it starts earlier (4 days vs. 5 days).

#### Rule Application

Expiration is performed separately for each backup set. If a backup set does
not have an expiration policy, all archives in that set are retained
unconditionally. Otherwise, any archive _not_ selected by its governing rule is
marked for expiration. Any archive that has no applicable rules is retained
unconditionally.

You can view a log of the rule evaluation without effecting any actual changes
by invoking `snapback -prune -dry-run`.

[ts]: https://www.tarsnap.com/
[tsdl]: https://www.tarsnap.com/download.html
[godl]: https://golang.org/doc/install
[yaml]: https://yaml.org/
