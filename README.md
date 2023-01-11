# The Image Controller for Stonesoup
The Image Controller for Stonesoup helps set up container image repositories for StoneSoup `Components`. 

## Try it!

### Installation

1. Install the project on your cluster by running `make deploy`. 
2. Set up the Quay.io token that would be used by the controller to setup image repositories under the `quay.io/redhat-user-workloads` organization.

```
kind: Secret
apiVersion: v1
metadata:
  name: quaytoken
  namespace: image-controller-system
data:
  organization: quay.io/redhat-user-workloads
  quaytoken: redacted
type: Opaque
```


### Create a Component

To request the controller to setup an image repository, annotate the `Component` with `appstudio.redhat.com/generate-image-repo: 'true'`.


```
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  annotations:
    appstudio.redhat.com/generate-image-repo: 'true'
  name: billing
  namespace: image-controller-system
spec:
  application: city-transit
  componentName: billing
```

### Verify 

The `Image controller` would create the necessary resources on Quay.io and write out the details of the same into the `Component` resource as an annotation, namely: 

* The image repository URL.
* The name of the Kubernets `Secret` in which the robot account token was written out to.

```
{
   "image_repository_url":"quay.io/redhat-user-workloads/image-controller-system/city-transit/billing",
   "credentials_kubernetes_secret":"billing-e562a75d-17dc-4c10-a2e6-924e120f6419",
}
```

```
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  annotations:
    appstudio.redhat.com/generate-image-repo: 'false'
    appstudio.redhat.com/generated-image-repository: >-
      {"image_repository_url":"quay.io/redhat-user-workloads/image-controller-system/city-transit/billing","credentials_kubernetes_secret":"billing-e562a75d-17dc-4c10-a2e6-924e120f6419"
      }
  name: billing
  namespace: image-controller-system
  resourceVersion: '86424'
  uid: 0e0f30b6-d77e-406f-bfdf-5802db1447a4
spec:
  application: city-transit
  componentName: billing
```

## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

