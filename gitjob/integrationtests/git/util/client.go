package util

import (
	"bytes"
	"io"
	"net/http"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/json"
)

const (
	gogsUser = "test"
	gogsPass = "pass"
)

type Client struct {
	apiURL string
	token  string
}

func NewClient(url string) (*Client, error) {
	token, err := createGogsToken(url)
	if err != nil {
		return nil, err
	}

	return &Client{apiURL: url + "/api/v1/", token: token}, nil
}

func (c *Client) AddPublicKey(publicKey string) error {
	values := map[string]string{"title": "testKey", "key": publicKey}
	jsonValue, _ := json.Marshal(values)
	client := &http.Client{}
	req, err := http.NewRequest(http.MethodPost, c.apiURL+"user/keys", bytes.NewBuffer(jsonValue))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 201 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(body))
	}

	return nil
}

func createGogsToken(url string) (string, error) {
	tokenURL := url + "/api/v1/users/test/tokens"
	values := map[string]string{"name": "token"}
	jsonValue, _ := json.Marshal(values)
	client := &http.Client{}

	req, err := http.NewRequest(http.MethodPost, tokenURL, bytes.NewBuffer(jsonValue))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(gogsUser, gogsPass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	tokenResponse := &tokenResponse{}
	err = json.Unmarshal(body, tokenResponse)
	if err != nil {
		return "", err
	}

	return tokenResponse.Sha1, nil
}

type tokenResponse struct {
	Name string `json:"name,omitempty"`
	Sha1 string `json:"sha1,omitempty"`
}
