[![codecov](https://codecov.io/gh/konflux-ci/image-controller/branch/main/graph/badge.svg)](https://codecov.io/gh/konflux-ci/image-controller)
# The Image Controller for AppStudio
The Image Controller operator helps set up container image repositories on Quay.io for AppStudio.

### Image Controller operator installation

1. Install the project on your cluster by running `make deploy`.
2. Set up the Quay.io token that would be used by the controller to set up image repositories under the `quay.io/redhat-user-workloads` organization.

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

## General purpose image repository

### Requesting image repository

To request an image repository one should create `ImageRepository` custom resource:
```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: ImageRepository
metadata:
  name: imagerepository-sample
  namespace: test-ns
```
As a result, a public image repository `quay.io/my-org/test-ns/imagerepository-sample` will be created.
When `status.state` is set to `ready`, the image repository is ready for use.
Additional information about the image repository one may obtain from the `ImageRepository` object `status`:
```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: ImageRepository
metadata:
  name: imagerepository-sample
  namespace: test-ns
spec:
  image:
    name: test-ns/imagerepository-sample
    visibility: public
status:
  credentials:
    generationTimestamp: "2023-08-23T14:56:41Z"
    push-robot-account: test_ns_imagerepository_sample_101e4e2b63
    push-remote-secret: imagerepository-sample-image-push
    push-secret: imagerepository-sample-image-push
  image:
    url: quay.io/my-org/test-ns/imagerepository-sample
    visibility: public
  state: ready
```
where:
  - `push-robot-account` is the name of quay robot account in the configured quay organization with write permissions to the repository.
  - `push-remote-secret` is an instance of `RemoteSecret` that manages the `Secret` specified in `push-secret`.
  - `push-secret` is a `Secret` of dockerconfigjson type that contains image repository push robot account token with write permissions.

### User defined image repository name

One may request custom image repository name by setting `spec.image.name` field upon the `ImageRepository` object creation.
Note, it's not possible to change image repository name after creation.
Any changes to the field will be reverted by the operator.

---
**NOTE**

Image repository name is always prefixed with the `ImageRepository` namespace.
The namespace prefix is separated by `/`.

---

### Image repository visibility

It's possible to control image repository visibility by `spec.image.visibility` field.
Allowed values are:
 - `public` (default)
 - `private`

It's possible to change the visibility at any time.

---
**NOTE**

Your quay.io organization plan should allow creation of private repositories.
In case your quay.io organization has free plan that does not allow setting repositories as private, then if `private` visibility was requested:
 - on `ImageRepository` creation, then the creation will fail.
 - after `ImageRepository` creation, then `status.message` will be set and `spec.image.visibility` reverted back to `public`.

---

### Credentials rotation

It's possible to request robot account token rotation by adding:
```yaml
...
spec:
  ...
  credentials:
    regenerate-token: true
  ...
```
After token rotation, the `spec.credentials.regenerate-token` section will be deleted and `status.credentials.generationTimestamp` updated.

---

### Verify and fix secrets links

It will link secret to service account if link is missing.
It will remove duplicate links of secret in service account.
It will remove secret from imagePullSecrets in service account.
It will unlink secret from service account, if secret doesn't exist (you can recreate secret using 'regenerate-token').
It's possible to request verification and fixing of secrets linking to service account by adding:
```yaml
...
spec:
  ...
  credentials:
    verify-linking: true
  ...
```
After verification, the `spec.credentials.verify-linking` section will be deleted.

---

### Error handling

If a critical error happens on image repository creation, then `status.state` is set to `failed` along with `status.message` field.
To retry image repository provision, one should recreate `ImageRepository` object.

If a non-critical error happens, then `status.message` is set and corresponding `spec` fields are reverted.

---
**NOTE**

After any successful operation, `status.message` is cleared.

---

### Skip repository deletion

By default, if the ImageRepository resource is deleted, the repository it created
in Quay will get deleted as well.

In order to skip the deletion of the repository in Quay, the `image-controller.appstudio.redhat.com/skip-repository-deletion` annotation should be set to "true".

## AppStudio Component image repository

### Image repository for Component builds

There is a special use case for image repository that stores user's `Component` built images.

To request image repository provision for the `Component`'s builds, the following labels must be added on `ImageRepository` creation:
 - `appstudio.redhat.com/component` with the `Component` name as the value
 - `appstudio.redhat.com/application` with the `Application` name to which the `Component` belongs to.

---
**NOTE**

Described above labels must be added on `ImageRepository` object creation.
Adding them later will have no effect.

---

The key differences from the general purpose workflow are:
 - second robot account and the corresponding `RemoteSecret` and `Secret` are created with read (pull) only access to the image repository.
 - the pull secret is propagated into all `Application` environments via `RemoteSecret`.
 - the secret with write (push) credentials is linked to the pipeline service account, so the `Component` build pipeline can push resulting images.

If `spec.image.name` is omitted, then instead of `ImageRepository` object name, `application-name/component-name` is used for the image repository name.

All other functionality is the same as for general purpose object.

### Requesting image repository for Component builds

To request an image repository for storing `Component` built images, one should create `ImageRepository` custom resource:
```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: ImageRepository
metadata:
  name: imagerepository-for-component-sample
  namespace: test-ns
  labels:
    appstudio.redhat.com/component: my-component
    appstudio.redhat.com/application: my-app
```
As a result, a public image repository `quay.io/my-org/test-ns/my-app/my-component` will be created.
When `status.state` is set to `ready`, the image repository is ready for use.
Additional information about the image repository one may obtain from the `ImageRepository` object `status`:
```yaml
apiVersion: appstudio.redhat.com/v1alpha1
kind: ImageRepository
metadata:
  name: imagerepository-for-component-sample
  namespace: test-ns
spec:
  image:
    name: test-ns/my-app/my-component
    visibility: public
status:
  credentials:
    generationTimestamp: "2023-08-23T14:56:41Z"
    push-robot-account: test_ns_my_app_my_component_e290bac4d
    push-remote-secret: imagerepository-for-component-sample-image-push
    push-secret: imagerepository-for-component-sample-image-push
    pull-robot-account: test_ns_my_app_my_component_6a54e08b62_pull
    pull-remote-secret: imagerepository-for-component-sample-image-pull
    pull-secret: imagerepository-for-component-sample-image-pull
  image:
    url: quay.io/my-org/test-ns/my-app/my-component
    visibility: public
  state: ready
```

## Legacy (deprecated) Component image repository

To request the controller to set up an image repository for a component, annotate the `Component` with `image.redhat.com/generate: '{"visibility": "public"}'` or `image.redhat.com/generate: '{"visibility": "private"}'` depending on desired repository visibility.

```yaml
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

Subsequently, the visibility of the image repository could be changed by toggling the value of "visibility".

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
* The name of the Kubernetes `Secret` in which the robot account token was written out to.

```json
{
   "image":"quay.io/redhat-user-workloads/image-controller-system/city-transit/billing",
   "visibility":"public",
   "secret":"billing"
}
```

```yaml
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

