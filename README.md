[![codecov](https://codecov.io/gh/redhat-appstudio/image-controller/branch/main/graph/badge.svg)](https://codecov.io/gh/redhat-appstudio/image-controller)
# The Image Controller for AppStudio
The Image Controller for AppStudio helps set up container image repositories for AppStudio `Components`.

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
  organization: redhat-user-workloads
  quaytoken: redacted
type: Opaque
```

To generate organization-wide token:

1. Create your own organization on [Quay.io](https://quay.io)
2. Go to the organization and select applications, then create a new one.
3. Select the application and choose generate token.
4. Select `Administer organizations`, `Adminster repositories`, `Create Repositories` permissions.

### Create a Component

To request the controller to setup an image repository, annotate the `Component` with `image.redhat.com/generate: '{"visibility": "public"}'` or `image.redhat.com/generate: '{"visibility": "private"}'` depending on desired repository visibility.


```
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  annotations:
    image.redhat.com/generate: '{"visibility": "public"}'
  name: billing
  namespace: image-controller-system
spec:
  application: city-transit
  componentName: billing
```

The `image.redhat.com/generate` annotation will be deleted after processing. The visibility status will be shown in `visibility` field of `image.redhat.com/image` annotation.

Subsequently, the visiblity of the image repository could be changed by toggling the value of "visibilty".

---
**NOTE**

Your quay.io organization plan should allow creation of private repositories.
If your quay.io organization has free plan that does not allow setting repositories as private, then the error will be shown in `message` field of `image.redhat.com/image` annotation and the repository will not be created or will remain public.

---

If `Component`'s auto-generated image repository should be deleted after component deletion, add `image.redhat.com/delete-image-repo` annotation to the `Component`.

### Verify

The `Image controller` would create the necessary resources on Quay.io and write out the details of the same into the `Component` resource as an annotation, namely:

* The image repository URL.
* The image repository visibility.
* The name of the Kubernets `Secret` in which the robot account token was written out to.

```
{
   "image":"quay.io/redhat-user-workloads/image-controller-system/city-transit/billing",
   "visibility":"public",
   "secret":"billing",
}
```

```
apiVersion: appstudio.redhat.com/v1alpha1
kind: Component
metadata:
  annotations:
    image.redhat.com/generate: 'false'
    image.redhat.com/image: >-
      {"image":"quay.io/redhat-user-workloads/image-controller-system/city-transit/billing","visibility":"public","secret":"billing"}
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

