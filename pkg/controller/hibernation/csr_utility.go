package hibernation

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"

	certsv1beta1 "k8s.io/api/certificates/v1beta1"
)

// csrUtility implements the csrHelper interface for use in the hibernation controller
type csrUtility struct{}

func (*csrUtility) IsApproved(obj *certsv1beta1.CertificateSigningRequest) bool {
	return isApproved(obj)
}

// parseCSR extracts the CSR from the API object and decodes it.
func (*csrUtility) Parse(obj *certsv1beta1.CertificateSigningRequest) (*x509.CertificateRequest, error) {
	// extract PEM from request object
	block, _ := pem.Decode(obj.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block type must be CERTIFICATE REQUEST")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

func (*csrUtility) Approve(client kubeclient.Interface, csr *certsv1beta1.CertificateSigningRequest) error {

	// Remove any previous CertificateApproved condition
	newConditions := []certsv1beta1.CertificateSigningRequestCondition{}
	for _, c := range csr.Status.Conditions {
		if c.Type != certsv1beta1.CertificateApproved {
			newConditions = append(newConditions, c)
		}
	}

	// Add approved condition
	newConditions = append(newConditions, certsv1beta1.CertificateSigningRequestCondition{
		Type:           certsv1beta1.CertificateApproved,
		Reason:         "KubectlApprove",
		Message:        "This CSR was automatically approved by Hive",
		LastUpdateTime: metav1.Now(),
	})
	csr.Status.Conditions = newConditions
	_, err := client.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(context.TODO(), csr, metav1.UpdateOptions{})
	return err
}
