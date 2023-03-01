package name_test

import (
	"fmt"

	"github.com/rancher/fleet/pkg/name"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Name", func() {
	type test struct {
		arg    string
		result string
		n      int
	}

	Context("Limit", func() {
		tests := []test{
			{arg: "1234567", n: 5, result: "12345"},
			{arg: "1234567", n: 6, result: "123456"},
			{arg: "1234567", n: 7, result: "1234567"},
			{arg: "1234567", n: 8, result: "1234567"},
			{arg: "12345678", n: 8, result: "12345678"},
			{arg: "12345678", n: 7, result: "1-25d55"},
			{arg: "123456789", n: 8, result: "12-25f9e"},
		}

		It("matches expected results", func() {
			for _, t := range tests {
				r := name.Limit(t.arg, t.n)
				Expect(r).To(Equal(t.result), fmt.Sprintf("%#v", t))
			}
		})
	})
})
