package githelper

// GitRepoTemplate can be used as fleet.yaml
const GitRepoTemplate = `
kind: GitRepo
apiVersion: fleet.cattle.io/v1alpha1
metadata:
  name: gitrepo-test
spec:
  repo: {{.Repo}}
  clientSecretName: git-auth
  branch: {{.Branch}}
  paths:
  - examples
`

type GitRepo struct {
	Repo   string
	Branch string
}
