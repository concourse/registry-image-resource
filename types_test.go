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
		It("should set platform to default if not specified", func() {
			source := resource.Source{
				RawPlatform: &resource.PlatformField{OS: "some-os", Architecture: "some-arch"},
			}

			platform := source.Platform()
			Expect(platform.Architecture).To(Equal("some-arch"))
			Expect(platform.OS).To(Equal("some-os"))
		})

		It("should set platform to default if not specified", func() {
			var source resource.Source

			platform := source.Platform()
			Expect(platform.Architecture).To(Equal(runtime.GOARCH))
			Expect(platform.OS).To(Equal(runtime.GOOS))
		})
	})
})
