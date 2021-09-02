# Image scan

Image scan in fleet allows you to scan your image repository, fetch the desired image and update your git repository, 
without the need to manually update your manifests.

!!! hint "Experimental"
    This feature is considered as experimental feature.
    
Go to `fleet.yaml` and add the following section.

```yaml
imageScans:
# specify the policy to retrieve images, can be semver or alphabetical order 
- policy: 
    # if range is specified, it will take the latest image according to semver order in the range
    # for more details on how to use semver, see https://github.com/Masterminds/semver
    semver: 
      range: "*" 
    # can use ascending or descending order
    alphabetical:
      order: asc 

  # specify images to scan
  image: "your.registry.com/repo/image" 

  # Specify the tag name, it has to be unique in the same bundle
  tagName: test-scan

  # specify secret to pull image if in private registry
  secretRef:
    name: dockerhub-secret 

  # Specify the scan interval
  interval: 5m 
```

!!! note
    You can create multiple image scans in fleet.yaml.
    
Go to your manifest files and update the field that you want to replace. For example:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis-slave
spec:
  selector:
    matchLabels:
      app: redis
      role: slave
      tier: backend
  replicas: 2
  template:
    metadata:
      labels:
        app: redis
        role: slave
        tier: backend
    spec:
      containers:
      - name: slave
        image: <image>:<tag> # {"$imagescan": "test-scan"}
        resources:
          requests:
            cpu: 100m
            memory: 100Mi
        ports:
        - containerPort: 6379
```

!!! note
    There are multiple form of tagName you can reference. For example
    
    `{"$imagescan": "test-scan"}`: Use full image name(foo/bar:tag)
    
    `{"$imagescan": "test-scan:name"}`: Only use image name without tag(foo/bar)
    
    `{"$imagescan": "test-scan:tag"}`: Only use image tag
    
    `{"$imagescan": "test-scan:digest"}`: Use full image name with digest(foo/bar:tag@sha256...) 
    
Create a GitRepo that includes your fleet.yaml

```yaml
kind: GitRepo
apiVersion: {{fleet.apiVersion}}
metadata:
  name: my-repo
  namespace: fleet-local
spec:
  # change this to be your own repo
  repo: https://github.com/rancher/fleet-examples 
  # define how long it will sync all the images and decide to apply change
  imageScanInterval: 5m 
  # user must properly provide a secret that have write access to git repository
  clientSecretName: secret 
  # specify the commit pattern
  imageScanCommit:
    authorName: foo
    authorEmail: foo@bar.com
    messageTemplate: "update image"
```

Try pushing a new image tag, for example, `<image>:<new-tag>`. Wait for a while and there should be a new commit pushed into your git repository to change tag in deployment.yaml.
Once change is made into git repository, fleet will read through the change and deploy the change into your cluster.