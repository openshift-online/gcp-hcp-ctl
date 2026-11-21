package networking

import (
	"context"
	"fmt"
)

func (c *Client) RegionReady(ctx context.Context, newRouter bool) error {
	region, err := c.service.Regions.Get(c.projectID, c.region).Context(ctx).Do()
	if err != nil {
		return err
	}
	if region.Status != "UP" {
		return fmt.Errorf("selected compute region is unavailable")
	}
	for _, q := range region.Quotas {
		if newRouter && q.Metric == "ROUTERS" && q.Limit-q.Usage < 1 {
			return fmt.Errorf("owned router quota is unavailable")
		}
	}
	return nil
}
