//go:generate go tool -modfile ../../../gotools/mockgen/go.mod mockgen --build_flags=--mod=mod -destination=./fetch_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler GitFetcher

package mocks
