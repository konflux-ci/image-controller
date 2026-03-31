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

	"github.com/konflux-ci/image-controller/pkg/quay"
	g "github.com/onsi/gomega"
)

func Test_getCacheExcludedObjectsTypes(t *testing.T) {
	g.RegisterTestingT(t)

	objects := getCacheExcludedObjectsTypes()
	g.Expect(objects).ToNot(g.BeNil())
}

func Test_readConfig(t *testing.T) {
	createTempFileWithContent := func(t *testing.T, fileContent string) *os.File {
		tempFile, err := os.CreateTemp("", "config-test-*.txt")
		t.Cleanup(func() { os.Remove(tempFile.Name()) })
		g.Expect(err).ToNot(g.HaveOccurred())
		_, err = tempFile.Write([]byte(fileContent))
		g.Expect(err).ToNot(g.HaveOccurred())
		err = tempFile.Close()
		g.Expect(err).ToNot(g.HaveOccurred())
		return tempFile
	}

	t.Run("ReadFromEnvVarOnly", func(t *testing.T) {
		g.RegisterTestingT(t)

		envVarName := "TEST_CONFIG_VAR"
		envVarValue := "env-value"
		os.Setenv(envVarName, envVarValue)
		defer os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, "/nonexistent/path")
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(envVarValue))
	})

	t.Run("ReadFromFileOnly", func(t *testing.T) {
		g.RegisterTestingT(t)

		fileContent := "file-content"
		tempFile := createTempFileWithContent(t, fileContent)

		result, err := readConfig("", tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(fileContent))
	})

	t.Run("EnvVarTakesPrecedenceOverFile", func(t *testing.T) {
		g.RegisterTestingT(t)

		tempFile := createTempFileWithContent(t, "file-content")

		// Set environment variable with different value
		envVarName := "TEST_CONFIG_VAR"
		envVarValue := "env-value"
		os.Setenv(envVarName, envVarValue)
		defer os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(envVarValue), "env var should take precedence over file")
	})

	t.Run("FileWithWhitespaceShouldBeTrimmed", func(t *testing.T) {
		g.RegisterTestingT(t)

		fileContent := "  \n\t file-value \t\n  "
		tempFile := createTempFileWithContent(t, fileContent)

		result, err := readConfig("", tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal("file-value"))
	})

	t.Run("EnvVarWithWhitespaceShouldBeTrimmed", func(t *testing.T) {
		g.RegisterTestingT(t)

		envVarName := "TEST_CONFIG_VAR"
		envVarValue := "  \n\t env-value \t\n  "
		os.Setenv(envVarName, envVarValue)
		defer os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, "/nonexistent/path")
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal("env-value"))
	})

	t.Run("EmptyEnvVarNameShouldReadFromFile", func(t *testing.T) {
		g.RegisterTestingT(t)

		fileContent := "file-content"
		tempFile := createTempFileWithContent(t, fileContent)

		result, err := readConfig("", tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(fileContent))
	})

	t.Run("FileDoesNotExistShouldReturnEmpty", func(t *testing.T) {
		g.RegisterTestingT(t)

		result, err := readConfig("", "/nonexistent/path/to/config")
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(""))
	})

	t.Run("PathIsDirectoryShouldReturnError", func(t *testing.T) {
		g.RegisterTestingT(t)

		tempDir, err := os.MkdirTemp("", "config-test-dir-*")
		g.Expect(err).ToNot(g.HaveOccurred())
		defer os.Remove(tempDir)

		_, err = readConfig("", tempDir)
		g.Expect(err).To(g.HaveOccurred())
		g.Expect(err.Error()).To(g.ContainSubstring("is a directory"))
	})

	t.Run("BothEnvVarAndFileNotPresentShouldReturnEmpty", func(t *testing.T) {
		g.RegisterTestingT(t)

		envVarName := "NONEXISTENT_CONFIG_VAR"
		os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, "/nonexistent/path")
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(""))
	})

	t.Run("FileWithEmptyContentShouldReturnEmpty", func(t *testing.T) {
		g.RegisterTestingT(t)

		tempFile := createTempFileWithContent(t, "")

		result, err := readConfig("", tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(""))
	})

	t.Run("EnvVarWithEmptyValueShouldTryFile", func(t *testing.T) {
		g.RegisterTestingT(t)

		fileContent := "file-content"
		tempFile := createTempFileWithContent(t, fileContent)

		envVarName := "TEST_CONFIG_VAR"
		os.Setenv(envVarName, "")
		defer os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(fileContent), "empty env var should fall back to file")
	})

	t.Run("EnvVarNotSetShouldTryFile", func(t *testing.T) {
		g.RegisterTestingT(t)

		fileContent := "file-content"
		tempFile := createTempFileWithContent(t, fileContent)

		envVarName := "NONEXISTENT_CONFIG_VAR"
		os.Unsetenv(envVarName)

		result, err := readConfig(envVarName, tempFile.Name())
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(result).To(g.Equal(fileContent))
	})
}

func Test_readQuayConfig(t *testing.T) {
	t.Run("SuccessWithEnvVars", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Set environment variables
		os.Setenv("QUAY_API_URL", "https://test-quay.io/api/v1")
		os.Setenv("QUAY_ORG", "test-org")
		os.Setenv("QUAY_TOKEN", "test-token")
		defer func() {
			os.Unsetenv("QUAY_API_URL")
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		apiUrl, org, buildQuayClientFunc, err := readQuayConfig()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(apiUrl).To(g.Equal("https://test-quay.io/api/v1"))
		g.Expect(org).To(g.Equal("test-org"))
		g.Expect(buildQuayClientFunc).ToNot(g.BeNil())

		// Test that buildQuayClientFunc works
		quayClient, err := buildQuayClientFunc()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(quayClient).ToNot(g.BeNil())
	})

	t.Run("SuccessWithFiles", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Clean env vars
		os.Unsetenv("QUAY_API_URL")
		os.Unsetenv("QUAY_ORG")
		os.Unsetenv("QUAY_TOKEN")
		os.Unsetenv("QUAY_ADDITIONAL_CA")

		// Create temp files
		tempDir, err := os.MkdirTemp("", "quay-config-*")
		g.Expect(err).ToNot(g.HaveOccurred())
		defer os.RemoveAll(tempDir)

		os.Setenv("QUAY_SECRET_MOUNT_POINT", tempDir)
		defer func() { os.Unsetenv("QUAY_SECRET_MOUNT_POINT") }()

		apiUrlFile := tempDir + "/quayapiurl"
		orgFile := tempDir + "/organization"
		tokenFile := tempDir + "/quaytoken"

		err = os.WriteFile(apiUrlFile, []byte("https://file-quay.io/api/v1"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())
		err = os.WriteFile(orgFile, []byte("file-org"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())
		err = os.WriteFile(tokenFile, []byte("file-token"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())

		apiUrl, org, buildQuayClientFunc, err := readQuayConfig()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(apiUrl).To(g.Equal("https://file-quay.io/api/v1"))
		g.Expect(org).To(g.Equal("file-org"))
		g.Expect(buildQuayClientFunc).ToNot(g.BeNil())
	})

	t.Run("DefaultApiUrl", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Don't set QUAY_API_URL
		os.Unsetenv("QUAY_API_URL")
		os.Setenv("QUAY_ORG", "test-org")
		os.Setenv("QUAY_TOKEN", "test-token")
		defer func() {
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		apiUrl, org, buildQuayClientFunc, err := readQuayConfig()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(apiUrl).To(g.Equal("https://quay.io/api/v1"), "should use default API URL")
		g.Expect(org).To(g.Equal("test-org"))
		g.Expect(buildQuayClientFunc).ToNot(g.BeNil())
	})

	t.Run("FailsIfOrgIsNotSet", func(t *testing.T) {
		g.RegisterTestingT(t)

		os.Setenv("QUAY_SECRET_MOUNT_POINT", "/nonexistent")

		os.Setenv("QUAY_API_URL", "https://test-quay.io/api/v1")
		os.Setenv("QUAY_ORG", "")
		os.Setenv("QUAY_TOKEN", "test-token")
		defer func() {
			os.Unsetenv("QUAY_API_URL")
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		_, _, _, err := readQuayConfig()
		g.Expect(err).To(g.HaveOccurred())
		g.Expect(err.Error()).To(g.ContainSubstring("Quay Org is not set"))
	})

	t.Run("FailsIfOrgIsNotSet", func(t *testing.T) {
		g.RegisterTestingT(t)

		os.Setenv("QUAY_SECRET_MOUNT_POINT", "/nonexistent")

		os.Setenv("QUAY_API_URL", "https://test-quay.io/api/v1")
		os.Setenv("QUAY_ORG", "test-org")
		os.Setenv("QUAY_TOKEN", "")
		defer func() {
			os.Unsetenv("QUAY_API_URL")
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		_, _, _, err := readQuayConfig()
		g.Expect(err).To(g.HaveOccurred())
		g.Expect(err.Error()).To(g.ContainSubstring("Quay token is not provided"))
	})

	t.Run("ErrorFromBuildQuayHttpClient", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Set an invalid CA path to trigger error in buildQuayHttpClient
		os.Setenv("QUAY_ADDITIONAL_CA", "/nonexistent/ca/path.pem")
		os.Setenv("QUAY_API_URL", "https://test-quay.io/api/v1")
		os.Setenv("QUAY_ORG", "test-org")
		os.Setenv("QUAY_TOKEN", "test-token")
		defer func() {
			os.Unsetenv("QUAY_ADDITIONAL_CA")
			os.Unsetenv("QUAY_API_URL")
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		_, _, _, err := readQuayConfig()
		g.Expect(err).To(g.HaveOccurred())
		g.Expect(err.Error()).To(g.ContainSubstring("unable to build Quay http client"))
	})

	t.Run("TokenRotationViaEnv", func(t *testing.T) {
		g.RegisterTestingT(t)

		// Set initial token
		os.Setenv("QUAY_API_URL", "https://test-quay.io/api/v1")
		os.Setenv("QUAY_ORG", "test-org")
		os.Setenv("QUAY_TOKEN", "initial-token")
		defer func() {
			os.Unsetenv("QUAY_API_URL")
			os.Unsetenv("QUAY_ORG")
			os.Unsetenv("QUAY_TOKEN")
		}()

		_, _, buildQuayClientFunc, err := readQuayConfig()
		g.Expect(err).ToNot(g.HaveOccurred())

		// Get first client with initial token
		client1, err := buildQuayClientFunc()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(client1).ToNot(g.BeNil())
		c1 := client1.(*quay.QuayClient)
		g.Expect(c1.AuthToken).To(g.Equal("initial-token"))

		// Change token (simulating rotation)
		os.Setenv("QUAY_TOKEN", "rotated-token")

		// Get second client - should use new token
		client2, err := buildQuayClientFunc()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(client2).ToNot(g.BeNil())
		c2 := client2.(*quay.QuayClient)
		g.Expect(c2.AuthToken).To(g.Equal("rotated-token"))
	})

	t.Run("TokenRotationViaFile", func(t *testing.T) {
		g.RegisterTestingT(t)

		os.Unsetenv("QUAY_API_URL")
		os.Unsetenv("QUAY_ORG")
		os.Unsetenv("QUAY_TOKEN")

		tempDir, err := os.MkdirTemp("", "quay-config-*")
		g.Expect(err).ToNot(g.HaveOccurred())
		defer os.RemoveAll(tempDir)

		os.Setenv("QUAY_SECRET_MOUNT_POINT", tempDir)
		defer func() { os.Unsetenv("QUAY_SECRET_MOUNT_POINT") }()

		apiUrlFile := tempDir + "/quayapiurl"
		orgFile := tempDir + "/organization"
		tokenFile := tempDir + "/quaytoken"

		err = os.WriteFile(apiUrlFile, []byte("https://file-quay.io/api/v1"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())
		err = os.WriteFile(orgFile, []byte("file-org"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())
		err = os.WriteFile(tokenFile, []byte("initial-token"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())

		_, _, buildQuayClientFunc, err := readQuayConfig()
		g.Expect(err).ToNot(g.HaveOccurred())

		// Get first client with initial token
		client1, err := buildQuayClientFunc()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(client1).ToNot(g.BeNil())
		c1 := client1.(*quay.QuayClient)
		g.Expect(c1.AuthToken).To(g.Equal("initial-token"))

		// Change token (simulating rotation)
		err = os.WriteFile(tokenFile, []byte("rotated-token"), 0644)
		g.Expect(err).ToNot(g.HaveOccurred())

		// Get second client - should use new token
		client2, err := buildQuayClientFunc()
		g.Expect(err).ToNot(g.HaveOccurred())
		g.Expect(client2).ToNot(g.BeNil())
		c2 := client2.(*quay.QuayClient)
		g.Expect(c2.AuthToken).To(g.Equal("rotated-token"))
	})
}

func Test_buildQuayHttpClient(t *testing.T) {
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

func Test_getQuayHost(t *testing.T) {
	testCases := []struct {
		name          string
		apiUrl        string
		expected      string
		errorExpected bool
	}{
		{
			name:     "should parse quay.io host",
			apiUrl:   "https://quay.io/api/v1",
			expected: "quay.io",
		},
		{
			name:     "should parse self-hosted quay host",
			apiUrl:   "https://quay.local/api/v1",
			expected: "quay.local",
		},
		{
			name:     "should parse self-hosted quay host with port",
			apiUrl:   "https://quay.local:8443/api/v1",
			expected: "quay.local:8443",
		},
		{
			name:     "should parse quay host with different api version",
			apiUrl:   "https://quay.local/api/v2",
			expected: "quay.local",
		},
		{
			name:     "should parse quay host without subdomains",
			apiUrl:   "https://localhost/api/v1",
			expected: "localhost",
		},
		{
			name:     "should parse quay with http protocol",
			apiUrl:   "http://quay.local/api/v1",
			expected: "quay.local",
		},
		{
			name:          "should fail if api url is invalid",
			apiUrl:        "://invalid-url/api/v1",
			errorExpected: true,
		},
		{
			name:          "should fail if empty api url",
			apiUrl:        "",
			errorExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g.RegisterTestingT(t)

			quayHost, err := getQuayHost(tc.apiUrl)
			if tc.errorExpected {
				g.Expect(err).To(g.HaveOccurred())
			} else {
				g.Expect(err).ToNot(g.HaveOccurred())
				g.Expect(quayHost).To(g.Equal(tc.expected))
			}
		})
	}
}
