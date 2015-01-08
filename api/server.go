package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/emicklei/go-restful"
	"github.com/hashicorp/terraform/command"
	"github.com/icub3d/graceful"
)

const (
	CONFIGFILE = "terraform.tf"
	PLANFILE   = "terraform.tfplan"
	STATEFILE  = "terraform.tfstate"
)

type files struct {
	tempDir    string
	configFile string
	planFile   string
	stateFile  string
}

type Request struct {
	Config json.RawMessage
	Plan   []byte
	State  json.RawMessage
}

type Response struct {
	Plan     string          `json:",omitempty"`
	State    json.RawMessage `json:",omitempty"`
	Ask      string          `json:",omitempty"`
	Info     string          `json:",omitempty"`
	Output   string          `json:",omitempty"`
	Error    string          `json:",omitempty"`
	ExitCode int
}

func (c *ApiCommand) startApi(ip string, port int) {
	c.ShutdownCommandCh = make(chan struct{}, 1)
	c.registerEndpoints()

	go func() {
		<-c.ShutdownServerCh
		c.ShutdownCommandCh <- struct{}{}
		graceful.Close()
	}()

	graceful.ListenAndServe(fmt.Sprintf("%s:%d", ip, port), nil)
}

func (c *ApiCommand) registerEndpoints() {
	ws := new(restful.WebService)
	ws.
		Path("/").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)

	ws.Route(ws.PUT("/apply").To(c.apply))
	ws.Route(ws.DELETE("/apply").To(c.apply))
	ws.Route(ws.POST("/plan").To(c.plan))
	ws.Route(ws.DELETE("/plan").To(c.plan))
	ws.Route(ws.PUT("/refresh").To(c.refresh))

	restful.Add(ws)
}

func (c *ApiCommand) apply(req *restful.Request, resp *restful.Response) {
	f, code, err := c.createFiles(req)
	if err != nil {
		resp.WriteError(code, err)
		return
	}

	defer os.RemoveAll(f.tempDir)

	// Set the arguments to be passed to the command
	args := []string{
		"-backup=-",
		"-input=false",
		"-no-color",
		"-state=" + f.stateFile,
	}

	if f.planFile != "" {
		args = append(args, f.planFile)
	} else {
		args = append(args, f.tempDir)
	}

	outputs := NewApiUi()
	cmd := &command.ApplyCommand{
		Meta: c.apiMeta(c.Meta, outputs),
	}

	if req.Request.Method == "DELETE" {
		cmd.Destroy = true
	}

	r := c.processResults(cmd.Run(args), outputs)

	r.State, err = ioutil.ReadFile(f.stateFile)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError,
			fmt.Errorf("Failed to read state from disk: %s", err))
		return
	}

	resp.WriteAsJson(r)
}

func (c *ApiCommand) plan(req *restful.Request, resp *restful.Response) {
	f, code, err := c.createFiles(req)
	if err != nil {
		resp.WriteError(code, err)
		return
	}

	defer os.RemoveAll(f.tempDir)

	// As we are creating a new plan, make sure we have a plan filename
	f.planFile = filepath.Join(f.tempDir, PLANFILE)

	// Set the arguments to be passed to the command
	args := []string{
		"-backup=-",
		"-input=false",
		"-no-color",
		"-state=" + f.stateFile,
		"-out=" + f.planFile,
		f.tempDir,
	}

	if req.Request.Method == "DELETE" {
		args = append(args, "-destroy")
	}

	outputs := NewApiUi()
	cmd := &command.PlanCommand{
		Meta: c.apiMeta(c.Meta, outputs),
	}

	r := c.processResults(cmd.Run(args), outputs)

	plan, err := ioutil.ReadFile(f.planFile)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError,
			fmt.Errorf("Failed to read plan from disk: %s", err))
		return
	}
	r.Plan = base64.StdEncoding.EncodeToString(plan)

	r.State, err = ioutil.ReadFile(f.stateFile)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError,
			fmt.Errorf("Failed to read state from disk: %s", err))
		return
	}

	resp.WriteAsJson(r)
}

func (c *ApiCommand) refresh(req *restful.Request, resp *restful.Response) {
	f, code, err := c.createFiles(req)
	if err != nil {
		resp.WriteError(code, err)
		return
	}

	defer os.RemoveAll(f.tempDir)

	// Set the arguments to be passed to the command
	args := []string{
		"-backup=-",
		"-input=false",
		"-no-color",
		"-state=" + f.stateFile,
		f.tempDir,
	}

	outputs := NewApiUi()
	cmd := &command.RefreshCommand{
		Meta: c.apiMeta(c.Meta, outputs),
	}

	r := c.processResults(cmd.Run(args), outputs)

	r.State, err = ioutil.ReadFile(f.stateFile)
	if err != nil {
		resp.WriteError(http.StatusInternalServerError,
			fmt.Errorf("Failed to read state from disk: %s", err))
		return
	}

	resp.WriteAsJson(r)
}

func (c *ApiCommand) createFiles(req *restful.Request) (f *files, code int, err error) {
	// Decode the request body to get the required info
	var r Request
	err = req.ReadEntity(&r)
	if err != nil {
		return nil, http.StatusBadRequest,
			fmt.Errorf("Error parsing JSON: %s", err)
	}

	// Make a temp dir to hold the files for this call
	f = new(files)
	f.tempDir, err = ioutil.TempDir("", "terraform-")
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	defer func() {
		if err != nil {
			os.RemoveAll(f.tempDir)
		}
	}()

	// Check if we have a config and if so create the config file
	if len(r.Config) > 0 {
		f.configFile = filepath.Join(f.tempDir, CONFIGFILE)
		err = c.writeFile(f.configFile, bytes.NewReader(r.Config))
		if err != nil {
			return nil, http.StatusBadRequest,
				fmt.Errorf("Failed to save config to disk: %s", err)
		}
	}

	// Check if a plan is supplied and if so create the plan
	if len(r.Plan) > 0 {
		f.planFile = filepath.Join(f.tempDir, PLANFILE)
		err = c.writeFile(f.planFile, bytes.NewReader(r.Plan))
		if err != nil {
			return nil, http.StatusBadRequest,
				fmt.Errorf("Failed to save plan to disk: %s", err)
		}
	}

	// In all cases (so even when empty) create the state file
	f.stateFile = filepath.Join(f.tempDir, STATEFILE)
	err = c.writeFile(f.stateFile, bytes.NewReader(r.State))
	if err != nil {
		return nil, http.StatusBadRequest,
			fmt.Errorf("Failed to save state to disk: %s", err)
	}

	return f, http.StatusOK, nil
}

func (c *ApiCommand) writeFile(filePath string, content io.Reader) error {
	if err := os.MkdirAll(path.Dir(filePath), 0755); err != nil {
		return err
	}
	fo, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer fo.Close()

	if _, err := io.Copy(fo, content); err != nil {
		return err
	}
	return nil
}

type ApiUi struct {
	AskBuffer    *bytes.Buffer
	InfoBuffer   *bytes.Buffer
	OutputBuffer *bytes.Buffer
	ErrorBuffer  *bytes.Buffer
}

func NewApiUi() *ApiUi {
	return &ApiUi{
		AskBuffer:    new(bytes.Buffer),
		InfoBuffer:   new(bytes.Buffer),
		OutputBuffer: new(bytes.Buffer),
		ErrorBuffer:  new(bytes.Buffer),
	}
}

func (u *ApiUi) Ask(query string) (string, error) {
	u.AskBuffer.WriteString(query)
	return "", nil
}

func (u *ApiUi) Info(message string) {
	u.InfoBuffer.WriteString(message)
}

func (u *ApiUi) Output(message string) {
	u.OutputBuffer.WriteString(message)
}

func (u *ApiUi) Error(message string) {
	u.ErrorBuffer.WriteString(message)
}

// In order to catch the native output, we need to create a custom Meta
// instance that a redirects any output
func (c *ApiCommand) apiMeta(m command.Meta, ui *ApiUi) command.Meta {
	return command.Meta{
		Color:       m.Color,
		ContextOpts: m.ContextOpts,
		Ui:          ui,
	}
}

func (c *ApiCommand) processResults(exitCode int, outputs *ApiUi) *Response {
	return &Response{
		Ask:      outputs.AskBuffer.String(),
		Info:     outputs.InfoBuffer.String(),
		Output:   outputs.OutputBuffer.String(),
		Error:    outputs.ErrorBuffer.String(),
		ExitCode: exitCode,
	}
}
