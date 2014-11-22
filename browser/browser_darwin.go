package browser

import (
	"fmt"
	"github.com/karmatics/SlfSrvPlayground/config"
	"os"
	"os/exec"
	"strconv"
)

func LaunchDefaultBrowser(port int, secretKey string, initPathUrl string, initQuery string, verbose bool) {
	settings := config.GetSettings()
	var runSpec string
	if settings.SecretKeyInPath {
		runSpec = "http://127.0.0.1:" + strconv.Itoa(port) + "/" + secretKey + "/" + initPathUrl
	} else {
		runSpec = "http://127.0.0.1:" + strconv.Itoa(port) + "/" + initPathUrl
	}
	if initQuery != "" {
		runSpec += "?" + initQuery
	}
	if verbose {
		fmt.Println("open", runSpec)
	}
	cmd := exec.Command("open", runSpec)
	err := cmd.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
