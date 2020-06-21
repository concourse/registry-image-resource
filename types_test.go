package resource_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo"
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

		Expect(json).To(MatchJSON(`{"repository":"foo","tag":"0"}`))
	})
})
