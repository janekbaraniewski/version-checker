package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/jetstack/version-checker/pkg/api"
)

const (
	repoURL        = "https://registry.hub.docker.com/v2/repositories/%s/tags"
	imagePrefix    = "docker.io/"
	imagePrefixHub = "registry.hub.docker.com/"
)

type Options struct {
	LoginURL string
	Username string
	Password string
	JWT      string
}

type Client struct {
	*http.Client
	Options
}

type AuthResponse struct {
	Token string `json:"token"`
}

type TagResponse struct {
	Next    string   `json:"next"`
	Results []Result `json:"results"`
}

type Result struct {
	Name      string  `json:"name"`
	Timestamp string  `json:"last_updated"`
	Images    []Image `json:"images"`
}

type Image struct {
	Digest       string `json:"digest"`
	OS           string `json:"os"`
	Architecture string `json:"Architecture"`
}

func New(ctx context.Context, opts Options) (*Client, error) {
	client := &http.Client{
		Timeout: time.Second * 5,
	}

	// Setup Auth if username and password used.
	if len(opts.Username) > 0 || len(opts.Password) > 0 {
		if len(opts.JWT) > 0 {
			return nil, errors.New("cannot specify JWT as well as username/password")
		}

		token, err := basicAuthSetup(client, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to setup auth: %s", err)
		}
		opts.JWT = token
	}

	return &Client{
		Options: opts,
		Client:  client,
	}, nil
}

func (c *Client) IsClient(imageURL string) bool {
	return strings.HasPrefix(imageURL, imagePrefix) ||
		strings.HasPrefix(imageURL, imagePrefixHub)
}

func (c *Client) Tags(ctx context.Context, imageURL string) ([]api.ImageTag, error) {
	if strings.HasPrefix(imageURL, imagePrefix) {
		imageURL = strings.TrimPrefix(imageURL, imagePrefix)
	}

	if strings.HasPrefix(imageURL, imagePrefixHub) {
		imageURL = strings.TrimPrefix(imageURL, imagePrefixHub)
	}

	if len(strings.Split(imageURL, "/")) == 1 {
		imageURL = fmt.Sprintf("library/%s", imageURL)
	}

	url := fmt.Sprintf(repoURL, imageURL)

	var tags []api.ImageTag
	for url != "" {
		response, err := c.doRequest(ctx, url)
		if err != nil {
			return nil, err
		}

		for _, result := range response.Results {
			// No images in this result, so continue early
			if len(result.Images) == 0 {
				continue
			}

			timestamp, err := time.Parse(time.RFC3339Nano, result.Timestamp)
			if err != nil {
				return nil, fmt.Errorf("failed to parse image timestamp: %s", err)
			}

			for _, image := range result.Images {
				// Image without digest contains no real image.
				if len(image.Digest) == 0 {
					continue
				}

				tags = append(tags, api.ImageTag{
					Tag:          result.Name,
					SHA:          image.Digest,
					Timestamp:    timestamp,
					OS:           image.OS,
					Architecture: image.Architecture,
				})
			}
		}

		url = response.Next
	}

	return tags, nil
}

func (c *Client) doRequest(ctx context.Context, url string) (*TagResponse, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.URL.Scheme = "https"
	req = req.WithContext(ctx)
	if len(c.JWT) > 0 {
		req.Header.Add("Authorization", "JWT "+c.JWT)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get docker image: %s", err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	response := new(TagResponse)
	if err := json.Unmarshal(body, response); err != nil {
		return nil, fmt.Errorf("unexpected image tags response: %s", body)
	}

	return response, nil
}

func basicAuthSetup(client *http.Client, opts Options) (string, error) {
	upReader := strings.NewReader(
		fmt.Sprintf(`{"username": "%s", "password": "%s"}`,
			opts.Username, opts.Password,
		),
	)

	req, err := http.NewRequest(http.MethodPost, opts.LoginURL, upReader)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", errors.New(string(body))
	}

	response := new(AuthResponse)
	if err := json.Unmarshal(body, response); err != nil {
		return "", err
	}

	return response.Token, nil
}
