package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CI Suite Verification", func() {
	It("should pass to ensure the test suite is not empty", func() {
		Expect(true).To(BeTrue())
	})
})
