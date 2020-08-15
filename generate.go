//go:generate go run pkg/codegen/cleanup/main.go
//go:generate go run pkg/codegen/main.go
//go:generate go run main.go install manager --crds-only -o ./chart/crds/crds.yaml

package main
