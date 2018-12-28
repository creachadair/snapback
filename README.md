# snapback: A tarsnap backup tool

`snapback` is a command-line wrapper tool that makes it easier to manage backups on the [tarsnap][ts] service. Under the covers, it calls out to the `tarsnap` command-line tool to create, list, and delete backups, and can read settings from a configuration file.


## Installation

To use `snapback`, you will need [tarsnap][tsdl] and [Go][godl].  Once you have these installed, run:

```shell
$ env GO111MODULE=off go get bitbucket.org/creachadair/snapback
```

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

# Default expiration settings. These settings govern how old backups are
# cleaned up by snapback -prune.
expiration:
- latest: 3       # keep the latest three archives of every set

- after: 1 day    # after one day, keep one archive per day
  sample: 1/day

- after: 1 month  # after one month, keep one archive per week
  sample: 1/week

- after: 6 months # after six months, keep one archive per month.
  sample: 1/month

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
    latest: 1000     # one way to express "keep it all"
  - after: 10 days
	sample: 1/month
```

[ts]: https://www.tarsnap.com/
[tsdl]: https://www.tarsnap.com/download.html
[godl]: https://golang.org/doc/install
[yaml]: https://yaml.org/
