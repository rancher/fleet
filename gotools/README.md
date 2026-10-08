# Gotools

This directory contains Go-based tools to use with [go
tool](https://tip.golang.org/doc/modules/managing-dependencies#tools).

Each tool is within its own directory with its own `go.mod` file to avoid
dependency conflicts.

## Managing tools

**Using a tool**

```sh
go tool -modfile <path to modfile> <tool>
```

For example, to use mockgen:

```sh
go tool -modfile gotools/mockgen/go.mod mockgen -h
```

**Add a new tool**

From repository root:

```sh
TOOLNAME=<tool name>
mkdir -p gotools/"$TOOLNAME"
go mod init -modfile=gotools/"$TOOLNAME"/go.mod github.com/rancher/fleet/gotools/"$TOOLNAME"
go get -tool -modfile=gotools/"$TOOLNAME"/go.mod <module>@<version>
```

For example, mockgen was added this way:

```sh
TOOLNAME=mockgen
mkdir -p gotools/"$TOOLNAME"
go mod init -modfile=gotools/"$TOOLNAME"/go.mod github.com/rancher/fleet/gotools/"$TOOLNAME"
go get -tool -modfile=gotools/"$TOOLNAME"/go.mod go.uber.org/mock/mockgen@v0.6.0
```


**Update existing tool**

From repository root:

```sh
TOOLNAME=<tool name>
go get -tool -modfile=gotools/"$TOOLNAME"/go.mod <module>@<new version>
```

For example, to update mockgen to v0.6.0:

```sh
TOOLNAME=mockgen
go get -tool -modfile=gotools/"$TOOLNAME"/go.mod go.uber.org/mock/mockgen@v0.6.0
```
