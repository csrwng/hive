package e2e

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	hiveapis "github.com/openshift/hive/pkg/apis"
)

func init() {
	hiveapis.AddToScheme(scheme.Scheme)
}

func GetClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("cannot obtain client config: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("cannot obtain client: %v", err)
	}
	return c, nil
}

func GetConfig() (*rest.Config, error) {
	return config.GetConfig()
}

func GetKubeClient() (kclient.Interface, error) {
	cfg, err := GetConfig()
	if err != nil {
		return nil, err
	}
	return kclient.NewForConfig(cfg)
}
