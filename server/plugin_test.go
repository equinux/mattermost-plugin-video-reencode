package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFileWillBeUploaded(t *testing.T) {
	p := setupPlugin()
	p.configuration = &configuration{ConvertMOVToMP4: true}
	fileInfo := model.FileInfo{
		Extension: "mov",
		Name:      "test_assets/clip.mov",
	}
	file, err := os.Open(fileInfo.Name)
	require.NoError(t, err)
	var output bytes.Buffer
	info, str := p.FileWillBeUploaded(nil, &fileInfo, file, &output)
	assert.Empty(t, str)
	require.NotNil(t, info)
	assert.Equal(t, info.Extension, "mp4")
}

func setupPlugin() *Plugin {
	setupAPI := func() *plugintest.API {
		api := &plugintest.API{}
		api.On("LogDebug", mock.Anything).Maybe()
		api.On("LogInfo", mock.Anything).Maybe()
		api.On("LogWarn", mock.AnythingOfType("string"))
		return api
	}

	api := setupAPI()
	p := Plugin{}
	p.API = api

	return &p
}
