package webserver

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"github.com/karmatics/SlfSrvPlayground/bundle"
	"github.com/karmatics/SlfSrvPlayground/config"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type jsonResponse map[string]interface{} // from http://nesv.blogspot.com/2012/09/super-easy-json-http-responses-in-go.html

type webserverData struct {
	keepAliveChan   chan int
	verbose         bool
	fullRootPath    string
	initFile        string
	storeFilespec   string
	zipReader       *zip.Reader // will be nil if reading from raw files
	secretKey       string
	secretKeyInPath bool
}

func (r jsonResponse) String() (s string) {
	b, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		s = ""
		return
	}
	s = string(b)
	return
}

var mimeTypes map[string]string = map[string]string{
	"js":   "application/javascript;charset=UTF-8",
	"html": "text/html;charset=UTF-8",
	"htm":  "text/html;charset=UTF-8",
	"css":  "text/css",
	"png":  "image/png",
	"gif":  "image/gif",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
}

func isDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

func getMimeType(filespec string) (mimeType string) { // return "" if not known
	var lastDot int = strings.LastIndex(filespec, ".")
	if -1 != lastDot {
		var fileExtension = strings.ToLower(filespec[lastDot+1:])
		mimeType = mimeTypes[fileExtension]
	}
	return
}

func web_server_forever(wsData *webserverData,
	port int, rootPath string, myselfExecutable string) {
	var lenSecretKey int = len(wsData.secretKey)

	if wsData.verbose {
		fmt.Printf("Listen forever on port %d...\n", port)
	}

	errorHandler := func(w http.ResponseWriter, r *http.Request, status int) {
		w.WriteHeader(status)
		if status == http.StatusNotFound {
			fmt.Fprint(w, "404")
		}
	}

	self_serving_core_js_handler := func(w http.ResponseWriter, r *http.Request) {
		secretKey := wsData.secretKey
		if !wsData.secretKeyInPath {
			secretKey = ""
		}

		writeCoreJsFile(JsCoreTemplateParams{
			SecretKey:         secretKey,
			OS:                runtime.GOOS,
			Port:              strconv.Itoa(port),
			RootPath:          strings.Replace(filepath.FromSlash(wsData.fullRootPath), "\\", "\\\\", -1),
			InitFile:          wsData.initFile,
			Self:              strings.Replace(filepath.FromSlash(myselfExecutable), "\\", "\\\\", -1),
			JsonJS:            json2_jsselfServingJsSrc(),
			NativePromiseOnly: native_promise_only_jsselfServingJsSrc(),
		}, w)
	}

	favicon := func(w http.ResponseWriter, r *http.Request) {
		var s string = faviconData()
		w.Header().Set("Content-Type", "image/x-icon")
		fmt.Fprintf(w, "%s", s)
	}

	noSecretKeyHandler := func(w http.ResponseWriter, r *http.Request) {
		if wsData.verbose {
			fmt.Fprintf(os.Stderr, "    No secret key for URL request \"%s\"\n", r.URL)
		}
		errorHandler(w, r, http.StatusNotFound)
	}

	callHandler := func(w http.ResponseWriter, r *http.Request) {
		var jResp jsonResponse
		var err error
		var paramsUrl string

		if wsData.secretKeyInPath {
			paramsUrl = r.URL.Path[lenSecretKey+7:]
		} else {
			paramsUrl = r.URL.Path[6:]
		}
		var parts []string = strings.Split(paramsUrl, "/")
		var functionName string = parts[0]
		var timeout int
		if timeout, err = strconv.Atoi(parts[1]); err != nil {
			errorHandler(w, r, http.StatusBadRequest)
			return
		}
		if wsData.verbose && (functionName != "keepalive") && (functionName != "check_wait_status") {
			fmt.Printf("functionName \"%s\", timeout %d\n", functionName, timeout)
		}
		jResp, err = clientCallHandler(functionName, timeout, r.Body, wsData)

		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
			errorHandler(w, r, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, jResp)
	}

	serveFileIfExists := func(filePath string, webPath string, w http.ResponseWriter, r *http.Request) bool {
		filename := filepath.FromSlash(filePath)
		dirname := ""
		isDir := false
		if isDirectory(filename) {
			if strings.LastIndex(webPath, "/") != (len(webPath) - 1) {
				http.Redirect(w, r, webPath+"/", http.StatusFound)
				return true
			}
			isDir = true
			dirname = filename
			filename = filename + "/index.html"
		}

		file, err := os.Open(filename)
		if err != nil {
			if isDir {
				dir, err := os.Open(dirname)
				if err != nil {
					http.Error(w, "404 Not Found : Error while opening the directory.", 404)
					return false
				}
				defer dir.Close()
				showDirectoryIndex(dir, w, r)
				return true
			}
			return false
		}
		defer file.Close()
		_, filenameOnly := path.Split(filename)
		http.ServeContent(w, r, filenameOnly, time.Time{}, file)
		return true
	}

	rawFileHandler := func(w http.ResponseWriter, r *http.Request) {
		var err error
		var urlPath string

		if wsData.secretKeyInPath {
			urlPath = r.URL.Path[lenSecretKey+2:]
		} else {
			urlPath = r.URL.Path
		}

		if wsData.verbose {
			fmt.Printf(" -- server return response for %s\n", urlPath)
		}
		/* rjb
		        // need the cookie?
		        cookie, err := r.Cookie("cookiename")
		        if(err == nil) {
						  fmt.Printf(" -- cookie cookiename: %+v\n", cookie.Value)
						}
		*/

		var mimeType string = getMimeType(urlPath)
		if len(mimeType) != 0 {
			w.Header().Set("Content-Type", mimeType)
		}

		if wsData.zipReader != nil {
			// return data from zip file
			var data []byte

			var filespec string = urlPath

			data, err = bundle.ReadFile(wsData.zipReader, filespec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "    Unable to read file %s\n", filepath.FromSlash(filespec))
				errorHandler(w, r, http.StatusNotFound)
				return
			} else {
				if wsData.verbose {
					fmt.Printf("    Return zipped file %s\n", filepath.FromSlash(filespec))
				}
				fmt.Fprintf(w, "%s", data)
			}

		} else {
			// return RAW data from filesystem
			paths := config.GetPossibleFilePathsFromUrl(urlPath)
			success := false
			for i := 0; i < len(paths); i++ {
				if serveFileIfExists(paths[i], urlPath, w, r) == true {
					success = true
					break
				}
			}
			if !success {
				fmt.Fprintf(os.Stderr, "    Unable to read file %+v\n", paths)
				errorHandler(w, r, http.StatusNotFound)
			}
		}
	}

	if wsData.secretKeyInPath {
		http.HandleFunc("/call/"+wsData.secretKey+"/", callHandler)
		http.HandleFunc("/"+wsData.secretKey+"/slfsrv-core.js", self_serving_core_js_handler)
		http.HandleFunc("/"+wsData.secretKey+"/", rawFileHandler)
		http.HandleFunc("/", noSecretKeyHandler)
	} else {
		http.HandleFunc("/call/", callHandler)
		http.HandleFunc("/slfsrv-core.js", self_serving_core_js_handler)
		http.HandleFunc("/", rawFileHandler)
		_ = noSecretKeyHandler
	}

	http.HandleFunc("/favicon.ico", favicon)

	err := http.ListenAndServe(":"+strconv.Itoa(port), nil)
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)
}

func keep_aliver(keepAliveChan chan int, exitChan chan int, keepAliveSeconds int64) {
	// quit if not tickled within a few seconds

	maxwait := time.Second * time.Duration(keepAliveSeconds)
	for {
		select {
		case <-keepAliveChan:
		case <-time.After(maxwait):
			exitChan <- 1
		}
	}
}

func ListenAndServe(port int, secretKey string, zipReader *zip.Reader,
	rootPath string, fullRootPath string, initFile string,
	verbose bool, exitChan chan int, myselfExecutable string,
	storeFilespec string) int {

	settings := config.GetSettings()
	if settings.Port != 0 {
		port = settings.Port
	}

	if port == 0 {
		// search around for an available port
		for {
			port = 8000 + rand.Intn(1000) // 8000 -> 8999
			if verbose {
				fmt.Printf("Try port %d ", port)
			}
			psock, err := net.Listen("tcp", ":"+strconv.Itoa(port))
			if err == nil {
				psock.Close()
				if verbose {
					fmt.Printf(" OK\n")
				}
				break
			}
			fmt.Printf(" - not available\n")
		}
	}

	keepAliveChan := make(chan int)

	go web_server_forever(&webserverData{
		keepAliveChan:   keepAliveChan,
		verbose:         verbose,
		fullRootPath:    fullRootPath,
		initFile:        initFile,
		storeFilespec:   storeFilespec,
		zipReader:       zipReader,
		secretKey:       secretKey,
		secretKeyInPath: settings.SecretKeyInPath,
	}, port, rootPath, myselfExecutable)

	go keep_aliver(keepAliveChan, exitChan, settings.KeepAliveSeconds)

	return port
}

