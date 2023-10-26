//go:generate go run ./cmd/codegen/cleanup/main.go
//go:generate go run ./cmd/codegen/main.go
//go:generate bash ./cmd/codegen/hack/generate_and_sort_crds.sh ./charts/fleet-crd/templates/crds.yaml

package main
