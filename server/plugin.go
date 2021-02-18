package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
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

// FileWillBeUploaded converts any attached "mov" video file to "mp4" using
// ffmpeg.
func (p *Plugin) FileWillBeUploaded(_ *plugin.Context,
	info *model.FileInfo,
	file io.Reader,
	output io.Writer,
) (*model.FileInfo, string) {
	if info == nil {
		return nil, ""
	}

	configuration := p.getConfiguration()
	if !configuration.ConvertMOVToMP4 {
		return info, ""
	}

	p.API.LogDebug(fmt.Sprintf("Received info: %+v", *info))

	if strings.ToLower(info.Extension) == "mov" {
		data, err := ioutil.ReadAll(file)
		if err != nil {
			errMsg := "failed to read video: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		tmpfile, err := ioutil.TempFile("", filepath.Base(info.Name)+".*.mov")
		if err != nil {
			errMsg := "failed to create temp file: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		defer os.Remove(tmpfile.Name())

		if _, writeErr := tmpfile.Write(data); writeErr != nil {
			tmpfile.Close()
			errMsg := "failed to write temp file: " + writeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}
		if closeErr := tmpfile.Close(); closeErr != nil {
			errMsg := "failed to close temp file: " + closeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		tmpFilename := tmpfile.Name()
		newFileExtension := "mp4"
		convertedFilename := tmpfile.Name() + "." + newFileExtension
		cmd := exec.Command("ffmpeg", "-i", tmpFilename, "-vcodec", "h264",
			"-acodec", "mp2", convertedFilename,
		)
		p.API.LogDebug(fmt.Sprintf("Running command: %s", cmd.String()))
		err = cmd.Run()
		if err != nil {
			errMsg := "failed to run convert command: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		convertedData, err := ioutil.ReadFile(convertedFilename)
		if err != nil {
			errMsg := "failed to read video: " + err.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}
		defer os.Remove(convertedFilename)

		if _, writeErr := output.Write(convertedData); writeErr != nil {
			errMsg := "failed to write new video: " + writeErr.Error()
			p.API.LogWarn(errMsg)
			return nil, errMsg
		}

		newInfo := *info
		newInfo.MimeType = "video/mp4"
		newInfo.Extension = newFileExtension
		nameWithoutExtension := info.Name[:strings.LastIndex(info.Name, ".")]
		newInfo.Name = nameWithoutExtension + "." + newFileExtension

		p.API.LogDebug(fmt.Sprintf("Created new info: %+v", newInfo))

		return &newInfo, ""
	}

	return nil, ""
}

// MessageHasBeenPosted hook posts a new message with a preview gif if the
// original post contained a video file.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	p.API.LogDebug(fmt.Sprintf("Post: %+v Metadata: %+v Attachments: %+v",
		post, post.Metadata, post.Attachments()))

	configuration := p.getConfiguration()
	if !configuration.CreatePreviewImage {
		return
	}

	fileIds := model.StringArray{}
	for _, fileID := range post.FileIds {
		info, _ := p.API.GetFileInfo(fileID)
		if info == nil {
			p.API.LogDebug("Missing fileinfo, skipping...")
			continue
		}

		switch strings.ToLower(info.Extension) {
		case "mp4":
			fallthrough
		case "m4v":
			fallthrough
		case "mov":
			previewFileID, err := createPreviewFileUpload(info, post.ChannelId,
				fileID, p.API)
			if err != nil {
				errMsg := "failed to create preview file upload: " + err.Error()
				p.API.LogWarn(errMsg)
				continue
			}
			fileIds = append(fileIds, *previewFileID)
		default:
			p.API.LogDebug(fmt.Sprintf("Unsupported extension '%s', skipping...",
				info.Extension))
			continue
		}
	}

	if len(fileIds) == 0 {
		return
	}

	newPost := &model.Post{
		RootId:    post.Id,
		ParentId:  post.Id,
		ChannelId: post.ChannelId,
		UserId:    post.UserId,
	}
	newPost.FileIds = fileIds

	p.API.LogDebug(fmt.Sprintf("New post: %+v", newPost))

	_, postErr := p.API.CreatePost(newPost)
	if postErr != nil {
		errMsg := "failed to create post: " + postErr.Error()
		p.API.LogWarn(errMsg)
		return
	}
}

func createPreviewFileUpload(
	info *model.FileInfo,
	channelID, fileID string,
	api plugin.API,
) (*string, error) {
	data, appErr := api.GetFile(fileID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to get file: %w", appErr)
	}

	tmpfile, err := ioutil.TempFile("", info.Name+".*."+info.Extension)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	if _, writeErr := tmpfile.Write(data); writeErr != nil {
		tmpfile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", writeErr)
	}
	if closeErr := tmpfile.Close(); closeErr != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", closeErr)
	}
	defer os.Remove(tmpfile.Name())

	tmpFilename := tmpfile.Name()
	previewPathTmp := tmpFilename + "_preview.gif"
	thumbCmd := exec.Command("ffmpeg", "-i", tmpFilename, "-vf",
		"fps=6,scale=320:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse",
		"-loop", "0", previewPathTmp)
	api.LogDebug(fmt.Sprintf("Running command: %s", thumbCmd.String()))
	err = thumbCmd.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to create preview image: %w", err)
	}
	defer os.Remove(previewPathTmp)
	fileData, readErr := ioutil.ReadFile(previewPathTmp)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read preview image: %w", readErr)
	}
	thumbFileInfo, appErr := api.UploadFile(fileData, channelID, "preview.gif")
	if appErr != nil {
		return nil, fmt.Errorf("failed to upload preview image: %w", appErr)
	}
	return &thumbFileInfo.Id, nil
}
