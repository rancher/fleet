TARGETS := $(shell ls scripts)
SETUP_ENVTEST_VER := v0.0.0-20221214170741-69f093833822

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

$(TARGETS): .dapper
	./.dapper $@

.DEFAULT_GOAL := default

.PHONY: $(TARGETS)

install-setup-envtest: ## Install setup-envtest.
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VER)

setup-envtest: install-setup-envtest # Build setup-envtest
	@if [ $(shell go env GOOS) == "darwin" ]; then \
		$(eval KUBEBUILDER_ASSETS := $(shell setup-envtest use --use-env -p path --arch amd64 $(ENVTEST_K8S_VERSION))) \
		echo "kube-builder assets set using darwin OS"; \
	else \
		$(eval KUBEBUILDER_ASSETS := $(shell setup-envtest use --use-env -p path $(ENVTEST_K8S_VERSION))) \
		echo "kube-builder assets set using other OS"; \
	fi

integration-test: setup-envtest
	KUBEBUILDER_ASSETS="$(KUBEBUILDER_ASSETS)" go test ./integrationtests/...
