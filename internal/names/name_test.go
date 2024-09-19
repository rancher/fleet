package names_test

import (
	"fmt"

	"github.com/rancher/fleet/internal/names"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Name", func() {
	type test struct {
		arg    string
		result string
		n      int
	}

	const (
		str50 = "1234567890" + "1234567890" + "1234567890" + "1234567890" + "1234567890"
		str53 = str50 + "123"
		str63 = str50 + "1234567890" + "123"
	)

	Context("Limit", func() {
		tests := []test{
			{arg: "1234567", n: 5, result: "12345"},
			{arg: "1234567", n: 6, result: "123456"},
			{arg: "1234567", n: 7, result: "1234567"},
			{arg: "1234567", n: 8, result: "1234567"},
			{arg: "12345678", n: 8, result: "12345678"},
			{arg: "12345678", n: 7, result: "1-25d55"},
			{arg: "123456789", n: 8, result: "12-25f9e"},
			{arg: "1-3456789", n: 8, result: "1-9b657"}, // no double dash in the result
		}

		It("matches expected results", func() {
			for _, t := range tests {
				r := names.Limit(t.arg, t.n)
				Expect(r).To(Equal(t.result), fmt.Sprintf("%#v", t))
			}
		})
	})

	Context("HelmReleaseName", func() {
		tests := []test{
			{arg: str53, result: str53},
			{arg: str53 + "a", result: "12345678901234567890123456789012345678901234567-fdba4"},
			{arg: str63 + "a", result: "12345678901234567890123456789012345678901234567-eb12d"},
			{arg: "long-name-test-shortpath-with@char", result: "long-name-test-shortpath-with-char-031bab5e"},
			{arg: "long-name-test-shortpath-with+char", result: "long-name-test-shortpath-with-char-21c88393"},
			{arg: "long-name-test-0.App_ ", result: "long-name-test-0-app-5bf6b3fb"},
			{arg: "long-name-test--App_-_12.factor", result: "long-name-test-app-12-factor-0efbac37"},
			{arg: "bundle.name.example.com", result: "bundle-name-example-com-645ef821"},
			// no double dash in the result
			{arg: str53[0:46] + "-1234567", result: "1234567890123456789012345678901234567890123456-d0bce"},
		}

		It("matches expected results", func() {
			for _, t := range tests {
				r := names.HelmReleaseName(t.arg)
				Expect(r).To(Equal(t.result), fmt.Sprintf("%#v", t))
			}
		})
	})
})
