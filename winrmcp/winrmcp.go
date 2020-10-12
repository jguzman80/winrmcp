package winrmcp

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dylanmei/iso8601"
	"github.com/masterzen/winrm"
)

type Winrmcp struct {
	client *winrm.Client
	config *Config
}

type Config struct {
	Auth                  Auth
	Https                 bool
	Insecure              bool
	TLSServerName         string
	CACertBytes           []byte
	ConnectTimeout        time.Duration
	OperationTimeout      time.Duration
	MaxOperationsPerShell int
	TransportDecorator    func() winrm.Transporter
}

type Auth struct {
	User     string
	Password string
}

func New(addr string, config *Config) (*Winrmcp, error) {
	endpoint, err := parseEndpoint(addr, config.Https, config.Insecure, config.TLSServerName, config.CACertBytes, config.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = &Config{}
	}

	params := winrm.NewParameters(
		winrm.DefaultParameters.Timeout,
		winrm.DefaultParameters.Locale,
		winrm.DefaultParameters.EnvelopeSize,
	)

	if config.TransportDecorator != nil {
		params.TransportDecorator = config.TransportDecorator
	}

	if config.OperationTimeout.Seconds() > 0 {
		params.Timeout = iso8601.FormatDuration(config.OperationTimeout)
	}
	client, err := winrm.NewClientWithParameters(
		endpoint, config.Auth.User, config.Auth.Password, params)
	return &Winrmcp{client, config}, err
}

func (fs *Winrmcp) Copy(fromPath, toPath string) error {

	f, err := os.Open(fromPath)
	if err != nil {
		return fmt.Errorf("Couldn't read file %s: %v", fromPath, err)
	}

	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("Couldn't stat file %s: %v", fromPath, err)
	}

	if !fi.IsDir() {
		return fs.Write(toPath, f)
	} else {

		tempFile, err := tempFileName()
		tempPath := fmt.Sprintf("%s/%s", os.TempDir(), tempFile)

		log.Printf("Temp Compressed File: %s", tempPath)

		if err != nil {
			return fmt.Errorf("Error generating unique filename: %v", err)
		}

		ziperr := zipit(fromPath, tempPath)
		if ziperr != nil {
			return fmt.Errorf("Error Zipping directory: %s", ziperr)
		}

		temp, err := os.Open(tempPath)
		if err != nil {
			return fmt.Errorf("Couldn't read file %s: %v", tempPath, err)
		}
		defer temp.Close()

		return fs.Write(toPath, temp)
	}
}

func (fs *Winrmcp) Write(toPath string, src io.Reader) error {
	return doCopy(fs.client, fs.config, src, winPath(toPath))
}

func (fs *Winrmcp) List(remotePath string) ([]FileItem, error) {
	return fetchList(fs.client, winPath(remotePath))
}

type fileWalker struct {
	client  *winrm.Client
	config  *Config
	toDir   string
	fromDir string
}

func (fw *fileWalker) copyFile(fromPath string, fi os.FileInfo, err error) error {
	if err != nil {
		return err
	}

	if !shouldUploadFile(fi) {
		return nil
	}

	hostPath, _ := filepath.Abs(fromPath)
	fromDir, _ := filepath.Abs(fw.fromDir)
	relPath, _ := filepath.Rel(fromDir, hostPath)
	toPath := filepath.Join(fw.toDir, relPath)

	f, err := os.Open(hostPath)
	if err != nil {
		return fmt.Errorf("Couldn't read file %s: %v", fromPath, err)
	}

	return doCopy(fw.client, fw.config, f, winPath(toPath))
}

func zipit(source, target string) error {

	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	info, err := os.Stat(source)
	if err != nil {
		return nil
	}

	var baseDir string
	if info.IsDir() {
		baseDir = filepath.Base(source)
	}

	filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		if baseDir != "" {
			//header.Name = sanitizedName(filepath.Join(baseDir, strings.TrimPrefix(path, source)))
			header.Name = filepath.Join(baseDir, strings.TrimPrefix(path, source))

		}

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

func shouldUploadFile(fi os.FileInfo) bool {
	// Ignore dir entries and OS X special hidden file
	return !fi.IsDir() && ".DS_Store" != fi.Name()
}
