package common

import (
	"io"
	"net/http"

	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwarder starts port forwarding to a given pod
type PortForwarder struct {
	Namespace string
	Name      string
	Stdout    io.Writer
	Stderr    io.Writer
	Client    kubernetes.Interface
	Config    *restclient.Config
}

// ForwardPorts will forward a set of ports from a pod, the stopChan will stop the forwarding
// when it's closed or receives a struct{}
func (f *PortForwarder) ForwardPorts(ports []string, stopChan <-chan struct{}) error {
	req := f.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(f.Namespace).
		Name(f.Name).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(f.Config)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	// TODO: Make os.Stdout/Stderr configurable
	readyChan := make(chan struct{})
	fw, err := portforward.New(dialer, ports, stopChan, readyChan, f.Stdout, f.Stderr)
	if err != nil {
		return err
	}
	errChan := make(chan error)
	go func() { errChan <- fw.ForwardPorts() }()
	select {
	case <-readyChan:
		return nil
	case err = <-errChan:
		return err
	}
}
