package version

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	miloversion "go.miloapis.com/milo/pkg/version"
)

// NewCommand creates a version command that displays version information
func NewCommand() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  "Print version information for the Milo binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			versionInfo := miloversion.Get()

			switch output {
			case "json":
				data, err := json.MarshalIndent(versionInfo, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			case "yaml":
				data, err := yaml.Marshal(versionInfo)
				if err != nil {
					return err
				}
				fmt.Print(string(data))
			case "short":
				fmt.Printf("Milo %s\n", versionInfo.MiloVersion)
			default:
				fmt.Printf("Milo version: %s\n", versionInfo.MiloVersion)
				fmt.Printf("Kubernetes API version: %s\n", versionInfo.GitVersion)
				fmt.Printf("Git commit: %s\n", versionInfo.GitCommit)
				fmt.Printf("Git tree state: %s\n", versionInfo.GitTreeState)
				fmt.Printf("Build date: %s\n", versionInfo.BuildDate)
				fmt.Printf("Go version: %s\n", versionInfo.GoVersion)
				fmt.Printf("Compiler: %s\n", versionInfo.Compiler)
				fmt.Printf("Platform: %s\n", versionInfo.Platform)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format. One of: json|yaml|short")

	return cmd
}
