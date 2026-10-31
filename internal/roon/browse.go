package roon

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Browse calls com.roonlabs.browse:1/browse.
func (c *Client) Browse(ctx context.Context, opts BrowseOpts) (*BrowseResult, error) {
	c.mu.RLock()
	conn := c.pairedMoo
	if opts.ZoneOrOutputID == "" {
		opts.ZoneOrOutputID = c.currentZoneID
	}
	c.mu.RUnlock()

	if conn == nil {
		return nil, errors.New("not connected to Roon Core")
	}
	if opts.Hierarchy == "" {
		opts.Hierarchy = "browse"
	}

	var res BrowseResult
	msg, err := conn.Call(ctx, svcBrowse+"/browse", opts, &res)
	if err != nil {
		return nil, err
	}
	if msg.Name != "Success" {
		return nil, fmt.Errorf("browse error: %s", msg.Name)
	}
	return &res, nil
}

// Load calls com.roonlabs.browse:1/load.
func (c *Client) Load(ctx context.Context, opts LoadOpts) (*LoadResult, error) {
	c.mu.RLock()
	conn := c.pairedMoo
	c.mu.RUnlock()

	if conn == nil {
		return nil, errors.New("not connected to Roon Core")
	}
	if opts.Hierarchy == "" {
		opts.Hierarchy = "browse"
	}
	if opts.Count <= 0 {
		opts.Count = 100
	}

	var res LoadResult
	msg, err := conn.Call(ctx, svcBrowse+"/load", opts, &res)
	if err != nil {
		return nil, err
	}
	if msg.Name != "Success" {
		return nil, fmt.Errorf("load error: %s", msg.Name)
	}
	return &res, nil
}

// GlobalSearch executes a search across both the local library and connected streaming services (Tidal/Qobuz).
func (c *Client) GlobalSearch(ctx context.Context, query string) (*LoadResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is empty")
	}

	c.mu.RLock()
	zoneID := c.currentZoneID
	c.mu.RUnlock()

	// Step 1: Reset the "browse" hierarchy to root and check for a Search input_prompt item at root or under Library
	_, err := c.Browse(ctx, BrowseOpts{
		Hierarchy:      "browse",
		PopAll:         true,
		ZoneOrOutputID: zoneID,
	})
	if err != nil {
		return nil, err
	}

	rootLoad, err := c.Load(ctx, LoadOpts{
		Hierarchy: "browse",
		Offset:    0,
		Count:     100,
	})
	if err != nil {
		return nil, err
	}

	var searchItemKey string
	var libraryItemKey string
	for _, it := range rootLoad.Items {
		if it.InputPrompt != nil {
			searchItemKey = it.ItemKey
			break
		}
		if strings.EqualFold(it.Title, "Library") || strings.EqualFold(it.Title, "Search") {
			if strings.EqualFold(it.Title, "Search") && it.ItemKey != "" {
				searchItemKey = it.ItemKey
				break
			}
			libraryItemKey = it.ItemKey
		}
	}

	// If Search prompt is inside "Library", drill into Library to get its item_key
	if searchItemKey == "" && libraryItemKey != "" {
		_, err = c.Browse(ctx, BrowseOpts{
			Hierarchy:      "browse",
			ItemKey:        libraryItemKey,
			ZoneOrOutputID: zoneID,
		})
		if err == nil {
			libLoad, err := c.Load(ctx, LoadOpts{
				Hierarchy: "browse",
				Offset:    0,
				Count:     100,
			})
			if err == nil {
				for _, it := range libLoad.Items {
					if it.InputPrompt != nil || strings.EqualFold(it.Title, "Search") {
						searchItemKey = it.ItemKey
						break
					}
				}
			}
		}
	}

	if searchItemKey != "" {
		_, err = c.Browse(ctx, BrowseOpts{
			Hierarchy:      "browse",
			ItemKey:        searchItemKey,
			Input:          query,
			ZoneOrOutputID: zoneID,
		})
		if err != nil {
			return nil, err
		}
		return c.Load(ctx, LoadOpts{
			Hierarchy: "browse",
			Offset:    0,
			Count:     100,
		})
	}

	// Fallback: use dedicated "search" hierarchy directly
	_, err = c.Browse(ctx, BrowseOpts{
		Hierarchy:      "search",
		PopAll:         true,
		ZoneOrOutputID: zoneID,
	})
	if err == nil {
		sLoad, lerr := c.Load(ctx, LoadOpts{
			Hierarchy: "search",
			Offset:    0,
			Count:     100,
		})
		if lerr == nil {
			for _, it := range sLoad.Items {
				if it.InputPrompt != nil && it.ItemKey != "" {
					_, _ = c.Browse(ctx, BrowseOpts{
						Hierarchy:      "search",
						ItemKey:        it.ItemKey,
						Input:          query,
						ZoneOrOutputID: zoneID,
					})
					return c.Load(ctx, LoadOpts{
						Hierarchy: "search",
						Offset:    0,
						Count:     100,
					})
				}
			}
		}
	}

	return nil, errors.New("search prompt not found in Roon browse hierarchy")
}
