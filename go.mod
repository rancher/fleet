module github.com/rancher/fleet

go 1.22.0

toolchain go1.22.5

replace (
	github.com/imdario/mergo => github.com/imdario/mergo v0.3.16
	github.com/rancher/fleet/pkg/apis => ./pkg/apis
	helm.sh/helm/v3 => github.com/rancher/helm/v3 v3.15.2-fleet.0
)

require (
	github.com/Masterminds/semver/v3 v3.2.1
	github.com/Masterminds/sprig/v3 v3.2.3
	github.com/cheggaaa/pb v1.0.29
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/docker v27.1.0+incompatible
	github.com/evanphx/json-patch v5.9.0+incompatible
	github.com/go-git/go-billy/v5 v5.5.1-0.20240701174852-839dcd3ad096
	github.com/go-git/go-git/v5 v5.12.1-0.20240629213349-b00c68ab7140
	github.com/go-logr/logr v1.4.2
	github.com/go-playground/webhooks/v6 v6.3.0
	github.com/gobwas/glob v0.2.3
	github.com/gogits/go-gogs-client v0.0.0-20210131175652-1d7215cd8d85
	github.com/golang/mock v1.6.0
	github.com/google/go-cmp v0.6.0
	github.com/google/go-containerregistry v0.20.1
	github.com/gorilla/mux v1.8.1
	github.com/hashicorp/go-getter v1.7.5
	github.com/jpillora/backoff v1.0.0
	github.com/onsi/ginkgo/v2 v2.19.0
	github.com/onsi/gomega v1.33.1
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0
	github.com/otiai10/copy v1.14.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.19.1
	github.com/prometheus/client_model v0.6.1
	github.com/prometheus/common v0.55.0
	github.com/rancher/fleet/pkg/apis v0.10.0
	github.com/rancher/lasso v0.0.0-20240705194423-b2a060d103c1
	github.com/rancher/wrangler/v3 v3.0.0
	github.com/reugn/go-quartz v0.11.2
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.8.1
	github.com/stretchr/testify v1.9.0
	github.com/testcontainers/testcontainers-go v0.32.0
	go.uber.org/mock v0.4.0
	go.uber.org/zap v1.27.0
	golang.org/x/crypto v0.25.0
	golang.org/x/sync v0.7.0
	gopkg.in/go-playground/webhooks.v5 v5.17.0
	gopkg.in/yaml.v2 v2.4.0
	gotest.tools v2.2.0+incompatible
	helm.sh/helm/v3 v3.15.3
	k8s.io/api v0.30.2
	k8s.io/apiextensions-apiserver v0.30.2
	k8s.io/apimachinery v0.30.2
	k8s.io/cli-runtime v0.30.2
	k8s.io/client-go v0.30.2
	k8s.io/klog/v2 v2.130.1
	k8s.io/kube-openapi v0.0.0-20240620174524-b456828f718b
	k8s.io/kubectl v0.30.2
	k8s.io/kubernetes v1.30.3
	k8s.io/utils v0.0.0-20240502163921-fe8a2dddb1d0
	oras.land/oras-go/v2 v2.5.0
	sigs.k8s.io/cli-utils v0.35.0
	sigs.k8s.io/controller-runtime v0.18.4
	sigs.k8s.io/controller-tools v0.15.0
	sigs.k8s.io/kustomize/api v0.13.5-0.20230601165947-6ce0bf390ce3
	sigs.k8s.io/kustomize/kyaml v0.14.3-0.20230601165947-6ce0bf390ce3
	sigs.k8s.io/yaml v1.4.0
)

require (
	cloud.google.com/go v0.110.8 // indirect
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	cloud.google.com/go/iam v1.1.2 // indirect
	cloud.google.com/go/storage v1.30.1 // indirect
	dario.cat/mergo v1.0.0 // indirect
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230811130428-ced1acdcaa24 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/BurntSushi/toml v1.3.2 // indirect
	github.com/MakeNowJust/heredoc v1.0.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/squirrel v1.5.4 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Microsoft/hcsshim v0.11.5 // indirect
	github.com/ProtonMail/go-crypto v1.0.0 // indirect
	github.com/asaskevich/govalidator v0.0.0-20210307081110-f21760c49a8d // indirect
	github.com/aws/aws-sdk-go v1.44.122 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bgentry/go-netrc v0.0.0-20140422174119-9fd32a8b3d3d // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chai2010/gettext-go v1.0.2 // indirect
	github.com/cloudflare/circl v1.3.7 // indirect
	github.com/containerd/containerd v1.7.18 // indirect
	github.com/containerd/errdefs v0.1.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.14.3 // indirect
	github.com/cpuguy83/dockercfg v0.3.1 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.4 // indirect
	github.com/cyphar/filepath-securejoin v0.2.5 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/cli v25.0.1+incompatible // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.7.0 // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/evanphx/json-patch/v5 v5.9.0 // indirect
	github.com/exponent-io/jsonpath v0.0.0-20210407135951-1de76d718b3f // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-errors/errors v1.4.2 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-gorp/gorp/v3 v3.1.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/gobuffalo/flect v1.0.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/gomodule/redigo v2.0.0+incompatible // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20240625030939-27f56978b8b0 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.4 // indirect
	github.com/googleapis/gax-go/v2 v2.12.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/gosuri/uitable v0.0.4 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-safetemp v1.0.0 // indirect
	github.com/hashicorp/go-version v1.6.0 // indirect
	github.com/huandu/xstrings v1.4.0 // indirect
	github.com/imdario/mergo v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/jmoiron/sqlx v1.3.5 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/compress v1.17.4 // indirect
	github.com/lann/builder v0.0.0-20180802200727-47ae307949d0 // indirect
	github.com/lann/ps v0.0.0-20150810152359-62de8c46ede0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.13 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/patternmatcher v0.6.0 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/moby/sys/sequential v0.5.0 // indirect
	github.com/moby/sys/user v0.1.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pjbgf/sha1cd v0.3.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.12.0 // indirect
	github.com/rubenv/sql-migrate v1.5.2 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/shirou/gopsutil/v3 v3.23.12 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/shopspring/decimal v1.3.1 // indirect
	github.com/skeema/knownhosts v1.2.2 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/ulikunitz/xz v0.5.10 // indirect
	github.com/vbatts/tar-split v0.11.3 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0 // indirect
	go.opentelemetry.io/otel v1.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.24.0 // indirect
	go.opentelemetry.io/otel/trace v1.24.0 // indirect
	go.starlark.net v0.0.0-20230525235612-a134d8f9ddca // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20240613232115-7f521ea00fb8 // indirect
	golang.org/x/mod v0.18.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/oauth2 v0.21.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.org/x/term v0.22.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/api v0.128.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto v0.0.0-20231012201019-e917dd12ba7a // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20231002182017-d307bd883b97 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231016165738-49dd2c1f3d0b // indirect
	google.golang.org/grpc v1.59.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiserver v0.30.2 // indirect
	k8s.io/code-generator v0.30.2 // indirect
	k8s.io/component-base v0.30.2 // indirect
	k8s.io/gengo v0.0.0-20240310015720-9cff6334dab4 // indirect
	k8s.io/gengo/v2 v2.0.0-20240228010128-51d4e06bde70 // indirect
	oras.land/oras-go v1.2.5 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
)
