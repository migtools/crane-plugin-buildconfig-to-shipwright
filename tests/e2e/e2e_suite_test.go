package e2e

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	projectRoot string
)

func init() {
	flag.StringVar(&projectRoot, "project-root", "../..", "Path to plugin project root")
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BuildConfig to Shipwright Conversion Suite")
}
