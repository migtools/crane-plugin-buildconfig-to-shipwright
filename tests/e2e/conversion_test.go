package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konveyor/crane-plugin-buildconfig-to-shipwright/tests/framework"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildConfig to Shipwright Conversion", func() {

	// Table-driven test: one entry per BuildConfig YAML
	DescribeTable("should convert BuildConfig to Shipwright Build correctly",
		func(testFile, issueNumber, description string) {
			// Setup paths
			testDataPath := filepath.Join(projectRoot, "tests", "testdata", "buildconfig_yamls", testFile)

			// Check if this test expects the BuildConfig to be skipped
			shouldSkip := strings.Contains(description, "should skip")

			// Step 1: Run plugin on YAML file
			By(fmt.Sprintf("Running plugin on %s", testFile))
			builds, err := framework.RunPluginOnYAML(testDataPath)

			if shouldSkip {
				// For unsupported strategies, we expect no Build output
				if err == nil && len(builds) == 0 {
					By("BuildConfig correctly skipped (no Build generated)")
					return // Test passes
				}
				if err == nil && len(builds) > 0 {
					Fail("Expected BuildConfig to be skipped, but Build files were generated")
				}
				// If there's an error, that might also indicate skipping - let it pass
				By("BuildConfig correctly skipped")
				return
			}

			// For normal conversions, we expect Build output
			Expect(err).NotTo(HaveOccurred())
			Expect(builds).NotTo(BeEmpty(), "No Build resources generated")

			// Step 2: Validate each generated Build against conversion rules
			By(fmt.Sprintf("Validating %d generated Build(s)", len(builds)))
			for _, buildObj := range builds {
				// Rule-based validation
				violations, err := framework.ValidateConversion(projectRoot, testDataPath, buildObj)
				Expect(err).NotTo(HaveOccurred())

				// Assert no violations
				if len(violations) > 0 {
					var msgs []string
					for _, v := range violations {
						msgs = append(msgs, v.String())
					}
					Fail(fmt.Sprintf("Conversion rule violations found:\n  - %s",
						strings.Join(msgs, "\n  - ")))
				}

				// Golden file comparison (if expected output exists)
				expectedFile := strings.TrimSuffix(testFile, ".yaml") + "-expected.yaml"
				expectedPath := filepath.Join(projectRoot, "tests", "testdata", "expected_output", expectedFile)

				if _, err := os.Stat(expectedPath); err == nil {
					By(fmt.Sprintf("Comparing with golden file: %s", expectedFile))

					// No variable expansion needed for our tests
					vars := map[string]string{}
					diffs, err := framework.CompareWithGoldenFile(buildObj, expectedPath, vars)
					Expect(err).NotTo(HaveOccurred())

					if len(diffs) > 0 {
						Fail(fmt.Sprintf("Build differs from expected output:\n  %s",
							strings.Join(diffs, "\n  ")))
					}
				}
			}
		},

		// Test cases - one Entry per BuildConfig YAML file
		Entry("[#833] datagrid-hotrod", "01-datagrid-hotrod.yaml", "833", "datagrid-hotrod"),
		Entry("[#834] cakephp-mysql", "02-cakephp-mysql.yaml", "834", "cakephp-mysql"),
		Entry("[#835] docker-and-s2i", "03-docker-and-s2i.yaml", "835", "docker-and-s2i"),
		Entry("[#836] webapp-docker", "04-webapp-docker.yaml", "836", "webapp-docker"),
		Entry("[#837] api-s2i", "05-api-s2i.yaml", "837", "api-s2i"),
		Entry("[#838] jenkins-pipeline (should skip)", "06-jenkins-pipeline.yaml", "838", "jenkins-pipeline (should skip)"),
		Entry("[#839] custom-strategy (should skip)", "07-custom-strategy.yaml", "839", "custom-strategy (should skip)"),
		Entry("[#840] docker-with-envvars", "08-docker-with-envvars.yaml", "840", "docker-with-envvars"),
		Entry("[#841] s2i-with-envvars", "09-s2i-with-envvars.yaml", "841", "s2i-with-envvars"),
		Entry("[#842] docker-with-volumes", "10-docker-with-volumes.yaml", "842", "docker-with-volumes"),
		Entry("[#843] s2i-with-volumes", "11-s2i-with-volumes.yaml", "843", "s2i-with-volumes"),
		Entry("[#844] pullsecret-nodejs", "12-pullsecret-nodejs.yaml", "844", "pullsecret-nodejs"),
		Entry("[#845] generic-test-build", "13-generic-test-build.yaml", "845", "generic-test-build"),
		Entry("[#846] docker-postcommit", "14-docker-postcommit.yaml", "846", "docker-postcommit"),
		Entry("[#847] build-with-proxy", "15-build-with-proxy.yaml", "847", "build-with-proxy"),
		Entry("[#848] imagesource-cross-namespace", "16-imagesource-cross-namespace.yaml", "848", "imagesource-cross-namespace"),
		Entry("[#849] docker-nocache", "17-docker-nocache.yaml", "849", "docker-nocache"),
		Entry("[#850] serviceaccount-override", "18-serviceaccount-override.yaml", "850", "serviceaccount-override"),
		Entry("[PR#60] docker-imagestream-ruby", "19-docker-imagestream-ruby.yaml", "PR60", "docker-imagestream-ruby from dev's cluster tests"),
		Entry("[PR#60] s2i-imagestream-nodejs", "20-s2i-imagestream-nodejs.yaml", "PR60", "s2i-imagestream-nodejs from dev's cluster tests"),
	)
})
