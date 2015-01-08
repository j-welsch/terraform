package api

import (
	"flag"
	"strings"

	"github.com/hashicorp/terraform/command"
)

// ApiCommand is a special Command implementation that enables
// Terraform to run as a service providing a RESTful API endpoint.
type ApiCommand struct {
	command.Meta
	ShutdownCommandCh chan struct{}
	ShutdownServerCh  <-chan struct{}
}

func (c *ApiCommand) Run(args []string) int {
	var ip string
	var port int

	args = c.Meta.Process(args, false)

	cmdFlags := flag.NewFlagSet("api", flag.ContinueOnError)
	cmdFlags.StringVar(&ip, "ip", "127.0.0.1", "127.0.0.1")
	cmdFlags.IntVar(&port, "port", 8080, "8080")
	cmdFlags.Usage = func() { c.Ui.Error(c.Help()) }
	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	c.startApi(ip, port)

	return 0
}

func (c *ApiCommand) Help() string {
	helpText := `
Usage: terraform api [options]

  Run Terraform as a service providing a RESTful API endpoint.

Options:

  -ip=127.0.0.1           The IP address the service will bind to. Defaults
                          to 127.0.0.1.

  -port=8080              The port the service will bind to. Defaults to 8080.

`
	return strings.TrimSpace(helpText)
}

func (c *ApiCommand) Synopsis() string {
	return "Run Terraform as a service providing a RESTful API endpoint"
}
