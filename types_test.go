package resource_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/ecr/ecriface"
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

	Describe("ecr", func() {
		It("should exclude a registry id as part of the request for an authorization token when omitted", func() {
			source := resource.Source{
				Repository: "foo",
				AwsCredentials: resource.AwsCredentials{
					AwsAccessKeyId:     "foo",
					AwsSecretAccessKey: "bar",
					AwsRegion:          "us-east-1",
				},
			}

			m := &mockECR{}
			_, err := source.GetECRAuthorizationToken(m)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(m.getAuthorizationInput.RegistryIds)).To(Equal(0))
		})

		It("should include a registry id as part of the request for an authorization token when specified", func() {
			source := resource.Source{
				Repository: "foo",
				AwsCredentials: resource.AwsCredentials{
					AwsAccessKeyId:     "foo",
					AwsSecretAccessKey: "bar",
					AwsRegion:          "us-east-1",
					AWSECRRegistryId:   "012345678901",
				},
			}

			m := &mockECR{}
			_, err := source.GetECRAuthorizationToken(m)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(m.getAuthorizationInput.RegistryIds)).To(Equal(1))
			Expect(*m.getAuthorizationInput.RegistryIds[0]).To(Equal(source.AwsCredentials.AWSECRRegistryId))
		})
	})
})

type mockECR struct {
	ecriface.ECRAPI

	getAuthorizationInput  *ecr.GetAuthorizationTokenInput
	getAuthorizationOutput *ecr.GetAuthorizationTokenOutput
	getAuthorizationError  error
}

func (m *mockECR) GetAuthorizationToken(input *ecr.GetAuthorizationTokenInput) (*ecr.GetAuthorizationTokenOutput, error) {
	m.getAuthorizationInput = input
	return m.getAuthorizationOutput, m.getAuthorizationError
}
