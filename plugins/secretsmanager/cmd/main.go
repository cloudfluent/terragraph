// Command secretsmanager serves the AWS Secrets Manager resolver or emits its build-time package descriptor.
package main

import (
	"encoding/json"
	"os"

	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/cloudfluent/terragraph/plugins/secretsmanager"
)

func main() {
	descriptor := secretsmanager.Descriptor()
	if len(os.Args) == 2 && os.Args[1] == "--descriptor" {
		if err := json.NewEncoder(os.Stdout).Encode(descriptor); err != nil {
			os.Exit(1)
		}
		return
	}
	sdk.Serve(descriptor, secretsmanager.Handler())
}
