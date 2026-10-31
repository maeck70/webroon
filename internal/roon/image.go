package roon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// FetchImage fetches artwork from the Roon Core HTTP image endpoint or MOO com.roonlabs.image:1/get_image.
func (c *Client) FetchImage(ctx context.Context, imageKey, scale string, width, height int) ([]byte, string, error) {
	c.mu.RLock()
	core := c.coreInfo
	conn := c.pairedMoo
	c.mu.RUnlock()

	if core == nil || imageKey == "" {
		return nil, "", errors.New("not connected or empty image_key")
	}

	if scale == "" {
		scale = "fit"
	}
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}

	// Fast path: HTTP image endpoint on Roon Core
	q := url.Values{}
	q.Set("scale", scale)
	q.Set("width", fmt.Sprintf("%d", width))
	q.Set("height", fmt.Sprintf("%d", height))
	q.Set("format", "image/jpeg")

	imgURL := fmt.Sprintf("http://%s:%s/api/image/%s?%s", core.Host, core.Port, url.PathEscape(imageKey), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err == nil {
		resp, err := c.httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				data, err := io.ReadAll(resp.Body)
				if err == nil && len(data) > 0 {
					ct := resp.Header.Get("Content-Type")
					if ct == "" {
						ct = "image/jpeg"
					}
					return data, ct, nil
				}
			}
		}
	}

	// Fallback: MOO com.roonlabs.image:1/get_image
	if conn != nil {
		msg, err := conn.Call(ctx, svcImage+"/get_image", map[string]any{
			"image_key": imageKey,
			"scale":     scale,
			"width":     width,
			"height":    height,
			"format":    "image/jpeg",
		}, nil)
		if err == nil && msg != nil && msg.Name == "Success" && len(msg.Body) > 0 {
			ct := msg.ContentType
			if ct == "" {
				ct = "image/jpeg"
			}
			return msg.Body, ct, nil
		}
	}

	return nil, "", errors.New("image not found")
}
