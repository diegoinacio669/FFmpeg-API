package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"ffmpeg-api/api"
	"ffmpeg-api/internal"
)

func main() {
	http.HandleFunc("/v1/process", handleProcess)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("server failed:", err)
	}
	log.Println("server listening on :8080")
}

func handleProcess(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodPost {
		internal.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()

	var req api.ProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Println("invalid request:", err)
		internal.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobPath, cleanup := createJobPath()
	defer cleanup()

	log.Println("job started:", jobPath)

	s3Client := internal.GetS3Client(&req.S3Config)

	if err := fetchInputs(s3Client, req.Inputs, jobPath); err != nil {
		log.Println("input fetch failed:", err)
		internal.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err, stdErr := runFFmpeg(req.Commands, jobPath); err != nil {
		log.Println("ffmpeg execution failed")
		for _, stdErrLine := range stdErr {
			log.Println("ffmpeg:", stdErrLine)
		}
		internal.WriteConsoleError(w, http.StatusInternalServerError, err.Error(), stdErr)
		return
	}

	if req.Output.InlineContentType != "" {
		log.Println("streaming inline output")
		streamFirstResult(w, r, jobPath, req)
		return
	}

	results, err := collectResults(s3Client, req, jobPath)
	if err != nil {
		log.Println("result collection failed:", err)
		internal.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("job finished in %s (%d outputs)", time.Since(start), len(results))
	internal.WriteJSON(w, http.StatusOK, api.ProcessResponse{Results: results})
}

func createJobPath() (string, func()) {
	path := filepath.Join(os.TempDir(), uuid.New().String())
	os.MkdirAll(path, 0755)
	return path, func() {
		log.Println("cleaning job dir:", path)
		os.RemoveAll(path)
	}
}

func fetchInputs(s3Client *s3.Client, inputs map[string]api.Input, jobPath string) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(inputs))

	for name, input := range inputs {
		wg.Add(1)
		go func(name string, input api.Input) {
			defer wg.Done()
			dst := filepath.Join(jobPath, name)
			log.Println("fetching input:", name)
			if err := fetchInput(s3Client, input, dst); err != nil {
				errCh <- fmt.Errorf("input %s: %w", name, err)
			}
		}(name, input)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		return err
	}
	return nil
}

func fetchInput(s3Client *s3.Client, input api.Input, dst string) error {
	switch {
	case input.S3 != "":
		return internal.DownloadFromS3(s3Client, input.S3, dst)
	case input.HTTP != "":
		return internal.DownloadFromHTTP(input.HTTP, dst)
	case input.Base64 != "":
		return internal.WriteBase64ToFile(input.Base64, dst)
	case input.Temporary:
		return nil
	default:
		return fmt.Errorf("input source missing")
	}
}

func runFFmpeg(commands [][]string, dir string) (error, []string) {
	for i, args := range commands {
		start := time.Now()
		sanitizedArgs := sanitizeArgs(args)
		log.Printf("ffmpeg step %d: %s", i+1, strings.Join(sanitizedArgs, " "))
		cmd := exec.Command("ffmpeg", sanitizedArgs...)
		cmd.Dir = dir

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return err, strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
		}

		log.Printf("ffmpeg step %d finished in %s", i+1, time.Since(start))
	}
	return nil, nil
}

func sanitizeArgs(args []string) []string {
	out := []string{"-hide_banner"}
	for _, a := range args {
		if a != "-hide_banner" {
			out = append(out, a)
		}
	}
	return out
}

func collectResults(s3Client *s3.Client, req api.ProcessRequest, jobPath string) (map[string]api.Result, error) {
	results := make(map[string]api.Result)

	files, _ := os.ReadDir(jobPath)
	for _, f := range files {
		if _, isInput := req.Inputs[f.Name()]; isInput {
			continue
		}

		log.Println("collecting output:", f.Name())

		path := filepath.Join(jobPath, f.Name())
		result := api.Result{}

		if req.Output.S3 != "" {
			url, err := internal.UploadToS3(s3Client, req.Output.S3, path, f.Name())
			if err != nil {
				return nil, err
			}
			result.URL = url
		}

		if req.Output.Base64 {
			data, _ := os.ReadFile(path)
			result.Base64 = base64.StdEncoding.EncodeToString(data)
		}

		results[f.Name()] = result
	}
	return results, nil
}

func streamFirstResult(w http.ResponseWriter, r *http.Request, jobPath string, req api.ProcessRequest) {
	files, _ := os.ReadDir(jobPath)
	for _, f := range files {
		if !f.IsDir() {
			if _, isInput := req.Inputs[f.Name()]; isInput {
				continue
			}
			log.Println("streaming file:", f.Name())
			w.Header().Set("Content-Type", req.Output.InlineContentType)
			http.ServeFile(w, r, filepath.Join(jobPath, f.Name()))
			return
		}
	}
}
