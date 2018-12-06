package resource_test

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Source", func() {
	It("should unmarshal tag int value into a string", func() {
		var source resource.Source
		raw := []byte(`{ "tag": 0 }`)

		err := json.Unmarshal(raw, &source)
		Expect(err).ToNot(HaveOccurred())
		Expect(source.Tag).To(Equal(resource.Tag("0")))
	})

	It("should unmarshal tag '' value to latest", func() {
		var source resource.Source
		raw := []byte(`{ "tag": "" }`)

		err := json.Unmarshal(raw, &source)
		Expect(err).ToNot(HaveOccurred())
		Expect(source.Tag).To(Equal(resource.Tag("latest")))
	})

	It("should marshal a tag back out to a string", func() {
		source := resource.Source{
			Tag: "0",
		}

		json, err := json.Marshal(source)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Contains(string(json[:]), `"tag":"0"`)).To(BeTrue())
	})
})
