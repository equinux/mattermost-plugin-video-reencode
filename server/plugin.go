package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/plugin"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration
	// and setConfiguration for usage.
	configuration *configuration
}

// FileWillBeUploaded is invoked when a file is uploaded, but before it is
// committed to backing store. Read from file to retrieve the body of the
// uploaded file.
func (p *Plugin) FileWillBeUploaded(_ *plugin.Context,
	info *model.FileInfo,
	file io.Reader,
	output io.Writer,
) (*model.FileInfo, string) {
	p.API.LogInfo(fmt.Sprintf("Received info: %+v", *info))

	if strings.ToLower(info.Extension) == "mov" {
		data, err := ioutil.ReadAll(file)
		if err != nil {
			errMsg := "Failed to read video: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		tmpfile, err := ioutil.TempFile("", info.Name+".*.mov")
		if err != nil {
			errMsg := "Failed to create temp file: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		defer os.Remove(tmpfile.Name())

		if _, writeErr := tmpfile.Write(data); writeErr != nil {
			tmpfile.Close()
			errMsg := "Failed to write temp file: " + writeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}
		if closeErr := tmpfile.Close(); closeErr != nil {
			errMsg := "Failed to close temp file: " + closeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		tmpFilename := tmpfile.Name()
		newFileExtension := "mp4"
		convertedFilename := tmpfile.Name() + "." + newFileExtension
		cmd := exec.Command("ffmpeg", "-i", tmpFilename, "-vcodec", "h264",
			"-acodec", "mp2", convertedFilename,
		)
		p.API.LogInfo(fmt.Sprintf("Running command: %s", cmd.String()))
		err = cmd.Run()
		if err != nil {
			errMsg := "Failed to run convert command: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		convertedData, err := ioutil.ReadFile(convertedFilename)
		if err != nil {
			errMsg := "Failed to read video: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}
		defer os.Remove(convertedFilename)

		if _, writeErr := output.Write(convertedData); writeErr != nil {
			errMsg := "Failed to write new video: " + writeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		newInfo := *info
		newInfo.MimeType = "video/mp4"
		newInfo.Extension = newFileExtension
		nameWithoutExtension := info.Name[:strings.LastIndex(info.Name, ".")]
		newInfo.Name = nameWithoutExtension + "." + newFileExtension

		configuration := p.getConfiguration()

		if configuration.CreatePreviewImage {
			thumbnailPathTmp := tmpFilename + "_thumb.jpg"
			// thumbnailPath := filepath.Dir(info.Path) + "/" + nameWithoutExtension + "_thumb.jpg"
			thumbCmd := exec.Command("ffmpeg", "-ss", "00:00:01", "-i", tmpFilename,
				"-vframes", "1", "-q:v", "2", thumbnailPathTmp,
			)
			out, outErr := thumbCmd.CombinedOutput()
			if outErr != nil {
				errMsg := "Failed to create combined output for command: " + outErr.Error()
				p.API.LogWarn(errMsg)
			}
			p.API.LogInfo(fmt.Sprintf("Running command: %s", thumbCmd.String()))
			err = thumbCmd.Run()
			if err == nil {
				newInfo.ThumbnailPath = thumbnailPathTmp
			} else {
				errMsg := "Failed to create thumbnail image: " + err.Error() + " " + string(out)
				p.API.LogWarn(errMsg)
			}

			previewPathTmp := tmpFilename + "_preview.jpg"
			// previewPath := filepath.Dir(info.Path) + "/" + nameWithoutExtension + "_preview.jpg"
			previewCmd := exec.Command("ffmpeg", "-ss", "00:00:01", "-i", tmpFilename,
				"-vframes", "1", "-q:v", "2", previewPathTmp,
			)
			out, outErr = previewCmd.CombinedOutput()
			if outErr != nil {
				errMsg := "Failed to create combined output for command: " + outErr.Error()
				p.API.LogWarn(errMsg)
			}
			p.API.LogInfo(fmt.Sprintf("Running command: %s", previewCmd.String()))
			err = previewCmd.Run()
			if err == nil {
				newInfo.PreviewPath = previewPathTmp
			} else {
				errMsg := "Failed to create preview image: " + err.Error() + " " + string(out)
				p.API.LogWarn(errMsg)
			}
		}

		p.API.LogInfo(fmt.Sprintf("Created new info: %+v", newInfo))

		return &newInfo, ""
	}

	return nil, ""
}
