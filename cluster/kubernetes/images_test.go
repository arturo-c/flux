package kubernetes

import (
	"encoding/base64"
	"testing"

	"github.com/go-kit/kit/log"
	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/weaveworks/flux/image"
	"github.com/weaveworks/flux/registry"
)

func noopLog(...interface{}) error {
	return nil
}

func makeImagePullSecret(ns, name, host string) *apiv1.Secret {
	imagePullSecret := apiv1.Secret{Type: apiv1.SecretTypeDockerConfigJson}
	imagePullSecret.Name = name
	imagePullSecret.Namespace = ns
	imagePullSecret.Data = map[string][]byte{
		apiv1.DockerConfigJsonKey: []byte(`
{
  "auths": {
    "` + host + `": {
      "auth": "` + base64.StdEncoding.EncodeToString([]byte("user:passwd")) + `"
      }
    }
}`),
	}
	return &imagePullSecret
}

func makeServiceAccount(ns, name string, imagePullSecretNames []string) *apiv1.ServiceAccount {
	sa := apiv1.ServiceAccount{}
	sa.Namespace = ns
	sa.Name = name
	for _, ips := range imagePullSecretNames {
		sa.ImagePullSecrets = append(sa.ImagePullSecrets, apiv1.LocalObjectReference{Name: ips})
	}
	return &sa
}

func TestMergeCredentials(t *testing.T) {
	ns, secretName1, secretName2 := "foo-ns", "secret-creds", "secret-sa-creds"
	saName := "service-account"
	ref, _ := image.ParseRef("foo/bar:tag")
	spec := apiv1.PodTemplateSpec{
		Spec: apiv1.PodSpec{
			ServiceAccountName: saName,
			ImagePullSecrets: []apiv1.LocalObjectReference{
				{Name: secretName1},
			},
			Containers: []apiv1.Container{
				{Name: "container1", Image: ref.String()},
			},
		},
	}

	clientset := fake.NewSimpleClientset(
		makeServiceAccount(ns, saName, []string{secretName2}),
		makeImagePullSecret(ns, secretName1, "docker.io"),
		makeImagePullSecret(ns, secretName2, "quay.io"))
	client := extendedClient{clientset, nil}

	creds := registry.ImageCreds{}
	cluster := NewCluster(clientset, nil, nil, nil, log.NewNopLogger(), []string{}, []string{})
	mergeCredentials(noopLog, cluster.filterImages, client, ns, spec, creds, make(map[string]registry.Credentials))

	// check that we accumulated some credentials
	assert.Contains(t, creds, ref.Name)
	c := creds[ref.Name]
	hosts := c.Hosts()
	assert.ElementsMatch(t, []string{"docker.io", "quay.io"}, hosts)
}

func TestMergeCredentials_ImageExclusion(t *testing.T) {
	creds := registry.ImageCreds{}
	gcrImage, _ := image.ParseRef("gcr.io/foo/bar:tag")
	k8sImage, _ := image.ParseRef("k8s.gcr.io/foo/bar:tag")
	testImage, _ := image.ParseRef("docker.io/test/bar:tag")

	spec := apiv1.PodTemplateSpec{
		Spec: apiv1.PodSpec{
			InitContainers: []apiv1.Container{
				{Name: "container1", Image: testImage.String()},
			},
			Containers: []apiv1.Container{
				{Name: "container1", Image: k8sImage.String()},
				{Name: "container2", Image: gcrImage.String()},
			},
		},
	}

	clientset := fake.NewSimpleClientset()
	client := extendedClient{clientset, nil}

	// set exclusion list
	cluster := NewCluster(clientset, nil, nil, nil, log.NewNopLogger(), []string{},
		[]string{"k8s.gcr.io/*", "*test*"})

	// filter images
	mergeCredentials(noopLog, cluster.filterImages, client, "default", spec, creds,
		make(map[string]registry.Credentials))

	// check test image has been excluded
	assert.NotContains(t, creds, testImage.Name)

	// check k8s.gcr.io image has been excluded
	assert.NotContains(t, creds, k8sImage.Name)

	// check gcr.io image exists
	assert.Contains(t, creds, gcrImage.Name)
}
