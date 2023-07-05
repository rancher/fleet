//go:generate go run cmd/codegen/cleanup/main.go
//go:generate go run cmd/codegen/main.go
//go:generate go run ./cmd/codegen crds ./charts/fleet-crd/templates/crds.yaml

package main
