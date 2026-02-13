package resource_test

import (
	"encoding/json"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Source", func() {
	It("should unmarshal tag float value into a string", func() {
		var source resource.Source
		raw := []byte(`{ "tag": 42.1 }`)

		err := json.Unmarshal(raw, &source)
		Expect(err).ToNot(HaveOccurred())
		Expect(source.Tag.String()).To(Equal("42.1"))
	})

	It("should unmarshal tag int value into a string", func() {
		var source resource.Source
		raw := []byte(`{ "tag": 42 }`)

		err := json.Unmarshal(raw, &source)
		Expect(err).ToNot(HaveOccurred())
		Expect(source.Tag.String()).To(Equal("42"))
	})

	It("should unmarshal tag string value into a string", func() {
		var source resource.Source
		raw := []byte(`{ "tag": "foo" }`)

		err := json.Unmarshal(raw, &source)
		Expect(err).ToNot(HaveOccurred())
		Expect(source.Tag.String()).To(Equal("foo"))
	})

	It("should marshal a tag back out to a string", func() {
		source := resource.Source{Repository: "foo", Tag: "0"}

		json, err := json.Marshal(source)
		Expect(err).ToNot(HaveOccurred())

		Expect(json).To(MatchJSON(`{"repository":"foo","insecure":false,"tag":"0"}`))
	})

	Describe("platform", func() {
		It("should set platform when specified in source", func() {
			source := resource.Source{
				RawPlatform: &resource.PlatformField{OS: "some-os", Architecture: "some-arch"},
			}

			platform := source.Platform(nil)
			Expect(platform.Architecture).To(Equal("some-arch"))
			Expect(platform.OS).To(Equal("some-os"))
		})

		It("should set platform when specified with step override", func() {
			source := resource.Source{
				RawPlatform: &resource.PlatformField{OS: "some-os", Architecture: "some-arch"},
			}

			platform := source.Platform(&resource.PlatformField{OS: "step-os", Architecture: "step-arch"})
			Expect(platform.Architecture).To(Equal("step-arch"))
			Expect(platform.OS).To(Equal("step-os"))
		})

		It("should set platform to default if not specified", func() {
			var source resource.Source

			platform := source.Platform(nil)
			Expect(platform.Architecture).To(Equal(runtime.GOARCH))
			Expect(platform.OS).To(Equal(runtime.GOOS))
		})
	})

	Describe("Azure credentials unmarshaling", func() {
		It("should unmarshal azure_acr and azure_client_id", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "myregistry.azurecr.io/myimage",
				"azure_acr": true,
				"azure_client_id": "test-client-id"
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureACR).To(BeTrue())
			Expect(source.AzureClientId).To(Equal("test-client-id"))
		})

		It("should unmarshal azure_acr without azure_client_id for system-assigned MI", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "myregistry.azurecr.io/myimage",
				"azure_acr": true
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureACR).To(BeTrue())
			Expect(source.AzureClientId).To(BeEmpty())
		})

		It("should default azure_acr to false when not provided", func() {
			var source resource.Source
			raw := []byte(`{"repository": "alpine"}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureACR).To(BeFalse())
		})

		It("should not conflict when both AWS and Azure fields are set", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "test",
				"aws_region": "us-east-1",
				"azure_acr": true
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AwsRegion).To(Equal("us-east-1"))
			Expect(source.AzureACR).To(BeTrue())
		})

		It("should unmarshal azure_environment for multi-cloud support", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "myregistry.azurecr.us/myimage",
				"azure_acr": true,
				"azure_environment": "AzureGovernment"
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureACR).To(BeTrue())
			Expect(source.AzureEnvironment).To(Equal("AzureGovernment"))
		})

		It("should unmarshal azure_tenant_id", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "myregistry.azurecr.io/myimage",
				"azure_acr": true,
				"azure_tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47"
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureACR).To(BeTrue())
			Expect(source.AzureTenantId).To(Equal("72f988bf-86f1-41af-91ab-2d7cd011db47"))
		})

		It("should default azure_tenant_id to empty when not provided", func() {
			var source resource.Source
			raw := []byte(`{
				"repository": "myregistry.azurecr.io/myimage",
				"azure_acr": true
			}`)
			err := json.Unmarshal(raw, &source)
			Expect(err).ToNot(HaveOccurred())
			Expect(source.AzureTenantId).To(BeEmpty())
		})
	})
})
