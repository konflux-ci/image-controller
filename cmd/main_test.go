/*
Copyright 2023-2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"testing"

	g "github.com/onsi/gomega"
)

func TestBuildQuayHttpClient(t *testing.T) {
	t.Run("NoCustomCA", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Ensure no custom CA is set
		os.Unsetenv("QUAY_ADDITIONAL_CA")

		client, err := buildQuayHttpClient()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(client).ToNot(g.BeNil())
		g.Expect(client.Transport).ToNot(g.BeNil())
	})

	t.Run("WithCustomCA", func(t *testing.T) {
		g.RegisterTestingT(t)

		const caCert = `-----BEGIN CERTIFICATE-----
MIIC7zCCAdegAwIBAgIUOg84HFz9WIspx4SwKjxTPWef6nowDQYJKoZIhvcNAQEL
BQAwFTETMBEGA1UEAxMKcXVheS5sb2NhbDAeFw0yNjAzMTExNTU5MDFaFw0yNjA2
MDkxNTU5MDFaMBUxEzARBgNVBAMTCnF1YXkubG9jYWwwggEiMA0GCSqGSIb3DQEB
AQUAA4IBDwAwggEKAoIBAQDHNVHP4I8lQUoEi+3efzovyFSXe5mV2nlz329TWhYb
XCR+Pc1t6O88leUwZWgJJlFnu2YGPy9ipHNO9YhLkc5QUnROeiw7HdCLOeR+xhU0
uxy83oCuQFxmXYEMoeynnN2ceEv7fawZvVSUaO8ud6rqYtqrolt7dkW2p8nHFDxT
b8gbu95tzZ+os9WjSi8DUKJRwU4WM8C7Rk2vGNqE93SvRtP9Qo4xAwE3wcDxixUQ
5L/TOo+Ui7DluY/aXlGlBTUFyh4oaWrz/NMF/m8EgVvoeMjTCAyiMDnKSAP4UOwb
fssGem0dfY/+65pcEAG7o8kvJB29g013oxkczpv6s17FAgMBAAGjNzA1MA4GA1Ud
DwEB/wQEAwIFoDAMBgNVHRMBAf8EAjAAMBUGA1UdEQQOMAyCCnF1YXkubG9jYWww
DQYJKoZIhvcNAQELBQADggEBAJnOlSgwAYZm6bmeLRY1Q5NvbOOvCeyohfXCkweD
Jl2ET2FIquNw0ZE99KDxHBf4CTTJehMx9eU6kL+GMVtY4k0UWMhKqurMVuE9bZIC
8rrBSIYDMs4abeZ/yN/9FzikagbAxWinHlMiKKNEod7NGnWk+iePjnxchMWYj3Eb
1Oremp531YzYv8Yflb+a6BlPEo9BQFGoB52kNNFR8JBqpqW68gh7LVFVbAYKPe+u
B91GoLE3HWzdNv2KDA+sdl5F/ZlGRZNwReU/gTP28fni4X7Qf9lR1Fd4umK5jsDU
cG3Kp1aafIjtenvEY2y9gYClEx7q4OIsN1Lw/OUQsUbTm50=
-----END CERTIFICATE-----`

		tempCaCertFile, err := os.CreateTemp("", "ca-cert-*.pem")
		defer os.Remove(tempCaCertFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred(), "failed to create CA cert test file")

		_, err = tempCaCertFile.Write([]byte(caCert))
		g.Expect(err).ToNot(g.HaveOccurred(), "failed to write to CA cert test file")
		err = tempCaCertFile.Close()
		g.Expect(err).ToNot(g.HaveOccurred())

		// Set the environment variable
		os.Setenv("QUAY_ADDITIONAL_CA", tempCaCertFile.Name())
		defer os.Unsetenv("QUAY_ADDITIONAL_CA")

		client, err := buildQuayHttpClient()

		g.Expect(err).ToNot(g.HaveOccurred(), "failed to create quay http client")
		g.Expect(client).ToNot(g.BeNil(), "quay http client is nil")
		g.Expect(client.Transport).ToNot(g.BeNil(), "client.Transport is nil")

		// Verify that TLS config was set
		transport, ok := client.Transport.(*http.Transport)
		g.Expect(ok).To(g.BeTrue(), "client.Transport is not *http.Transport")
		g.Expect(transport.TLSClientConfig).ToNot(g.BeNil(), "TLS config was not set")
		g.Expect(transport.TLSClientConfig.RootCAs).ToNot(g.BeNil(), "RootCAs was not set in TLS config")

		// Verify that the custom CA certificate is in the RootCAs pool
		// We do this by attempting to verify the certificate against the pool
		testCertPEM := []byte(caCert)
		certBlock, _ := pem.Decode(testCertPEM)
		g.Expect(certBlock).ToNot(g.BeNil(), "failed to decode PEM certificate")

		parsedCert, err := x509.ParseCertificate(certBlock.Bytes)
		g.Expect(err).ToNot(g.HaveOccurred(), "failed to parse certificate")

		// Verify the certificate against the RootCAs pool
		// For a self-signed CA certificate, it should verify against itself if it's in the pool
		opts := x509.VerifyOptions{
			Roots:     transport.TLSClientConfig.RootCAs,
			DNSName:   "", // Empty for CA verification
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}

		_, err = parsedCert.Verify(opts)
		g.Expect(err).ToNot(g.HaveOccurred(), "custom CA certificate was not properly added to RootCAs pool")
	})

	t.Run("InvalidCAPath", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Set an invalid CA path
		os.Setenv("QUAY_ADDITIONAL_CA", "/nonexistent/path/to/ca.pem")
		defer os.Unsetenv("QUAY_ADDITIONAL_CA")

		_, err := buildQuayHttpClient()
		g.Expect(err).To(g.HaveOccurred(), "should have failed due to invalid CA cert path")
	})

	t.Run("InvalidCACert", func(t *testing.T) {
		g.RegisterTestingT(t)

		const caCertInvalid = "invalid certificate content"

		tempCaCertFile, err := os.CreateTemp("", "ca-cert-*.pem")
		defer os.Remove(tempCaCertFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred(), "failed to create CA cert test file")

		_, err = tempCaCertFile.Write([]byte(caCertInvalid))
		g.Expect(err).ToNot(g.HaveOccurred(), "failed to write to CA cert test file")
		err = tempCaCertFile.Close()
		g.Expect(err).ToNot(g.HaveOccurred())

		os.Setenv("QUAY_ADDITIONAL_CA", tempCaCertFile.Name())
		defer os.Unsetenv("QUAY_ADDITIONAL_CA")

		_, err = buildQuayHttpClient()
		g.Expect(err).To(g.HaveOccurred(), "should have failed due to invalid CA cert")
	})
}
