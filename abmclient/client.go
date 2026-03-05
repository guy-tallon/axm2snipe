// Package abmclient wraps the abm library's Client with axm2snipe-specific
// logic: MDM server name resolution via device linkages, flattened AppleCare
// coverage, and a simplified constructor.
package abmclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zchee/abm"
)

var log = logrus.New()

// SetLogLevel sets the logger level for the abmclient package.
func SetLogLevel(level logrus.Level) {
	log.SetLevel(level)
}

// retryTransport wraps an http.RoundTripper with retry logic for 429 responses.
// This is needed because the abm library's PageIterator makes raw HTTP calls
// that bypass the doJSONRequest retry logic.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	initial    time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	backoff := t.initial
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt >= t.maxRetries {
			return resp, nil
		}
		resp.Body.Close()
		log.Debugf("Rate limited (429), retrying in %v (attempt %d/%d)", backoff, attempt+1, t.maxRetries)

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(backoff):
		}
		backoff *= 2

		// Reset body for retry
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
	}
}

// Client wraps an abm.Client with axm2snipe-specific helpers.
type Client struct {
	abm *abm.Client
}

// NewClient creates a new ABM client using the abm library for auth.
func NewClient(ctx context.Context, clientID, keyID, privateKey string) (*Client, error) {
	assertion, err := abm.NewAssertion(ctx, clientID, keyID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating ABM assertion: %w", err)
	}

	ts, err := abm.NewTokenSource(ctx, nil, clientID, assertion, "")
	if err != nil {
		return nil, fmt.Errorf("creating ABM token source: %w", err)
	}

	httpClient := &http.Client{
		Transport: &retryTransport{
			base:       http.DefaultTransport,
			maxRetries: 5,
			initial:    2 * time.Second,
		},
	}

	client, err := abm.NewClient(httpClient, ts)
	if err != nil {
		return nil, fmt.Errorf("creating ABM client: %w", err)
	}

	return &Client{abm: client}, nil
}

// Device wraps an abm.OrgDevice with the resolved MDM server name.
type Device struct {
	abm.OrgDevice
	AssignedServer string `json:"assignedServerName,omitempty"`
}

// AppleCareCoverage holds flattened AppleCare coverage data for easy access in sync.
type AppleCareCoverage struct {
	AgreementNumber        string    `json:"agreementNumber,omitempty"`
	ContractCancelDateTime time.Time `json:"contractCancelDateTime,omitempty"`
	Description            string    `json:"description,omitempty"`
	EndDateTime            time.Time `json:"endDateTime,omitempty"`
	IsCanceled             bool      `json:"isCanceled,omitempty"`
	IsRenewable            bool      `json:"isRenewable,omitempty"`
	PaymentType            string    `json:"paymentType,omitempty"`
	StartDateTime          time.Time `json:"startDateTime,omitempty"`
	Status                 string    `json:"status,omitempty"`
}

// ConnectionTest fetches a single device to verify the ABM connection.
// Returns the total device count.
func (c *Client) ConnectionTest(ctx context.Context) (int, error) {
	resp, err := c.abm.GetOrgDevices(ctx, &abm.GetOrgDevicesOptions{Limit: 1})
	if err != nil {
		return 0, err
	}
	total := 0
	if resp.Meta != nil {
		total = resp.Meta.Paging.Total
	}
	return total, nil
}

// GetMDMServers fetches all MDM servers and returns a map of server ID to name.
func (c *Client) GetMDMServers(ctx context.Context) (map[string]string, error) {
	resp, err := c.abm.GetMDMServers(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching MDM servers: %w", err)
	}
	servers := make(map[string]string, len(resp.Data))
	for _, s := range resp.Data {
		if s.Attributes != nil {
			servers[s.ID] = s.Attributes.ServerName
		}
	}
	return servers, nil
}

// BuildDeviceServerMap fetches all MDM servers and their device linkages
// to build a map from device ID to server name.
func (c *Client) BuildDeviceServerMap(ctx context.Context) (map[string]string, error) {
	serverNames, err := c.GetMDMServers(ctx)
	if err != nil {
		return nil, err
	}
	log.Infof("Found %d MDM servers for device linkage resolution", len(serverNames))

	deviceToServer := make(map[string]string)
	for serverID, serverName := range serverNames {
		resp, err := c.abm.GetMDMServerDeviceLinkages(ctx, serverID, &abm.GetMDMServerDeviceLinkagesOptions{Limit: 1000})
		if err != nil {
			log.WithError(err).WithField("server", serverName).Warn("Could not fetch device linkages for MDM server")
			continue
		}
		for _, linkage := range resp.Data {
			deviceToServer[linkage.ID] = serverName
		}
		log.Debugf("MDM server %q: %d device linkages", serverName, len(resp.Data))

		if resp.Links.Next != "" {
			log.Warnf("MDM server %q has more than 1000 device linkages; pagination not yet supported, some devices may not have server names resolved", serverName)
		}
	}
	log.Infof("Built device-to-server map with %d entries", len(deviceToServer))
	return deviceToServer, nil
}

// GetAllDevices fetches all devices, resolves assigned MDM server names.
func (c *Client) GetAllDevices(ctx context.Context) ([]Device, int, error) {
	// Build device ID → server name map
	deviceToServer, err := c.BuildDeviceServerMap(ctx)
	if err != nil {
		// Non-fatal: continue without server names
		deviceToServer = make(map[string]string)
	}

	// Fetch all devices
	orgDevices, total, err := c.abm.FetchAllOrgDevices(ctx)
	if err != nil {
		return nil, 0, err
	}

	// Wrap with resolved server names
	devices := make([]Device, len(orgDevices))
	for i, od := range orgDevices {
		devices[i] = Device{OrgDevice: od}
		if name, ok := deviceToServer[od.ID]; ok {
			devices[i].AssignedServer = name
		}
	}

	return devices, total, nil
}

// GetDevice fetches a single device by serial number and resolves its assigned MDM server name.
func (c *Client) GetDevice(ctx context.Context, serial string) (*Device, error) {
	resp, err := c.abm.GetOrgDevice(ctx, serial, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching device %s: %w", serial, err)
	}

	device := &Device{OrgDevice: resp.Data}

	// Resolve assigned MDM server name
	serverResp, err := c.abm.GetOrgDeviceAssignedServer(ctx, serial, nil)
	if err != nil {
		log.WithError(err).WithField("serial", serial).Debug("Could not fetch assigned MDM server")
	} else if serverResp.Data.Attributes != nil {
		device.AssignedServer = serverResp.Data.Attributes.ServerName
	}

	return device, nil
}

// GetAppleCareCoverage fetches AppleCare coverage for a device,
// returning a flattened struct or nil if no coverage exists.
func (c *Client) GetAppleCareCoverage(ctx context.Context, deviceID string) (*AppleCareCoverage, error) {
	resp, err := c.abm.GetOrgDeviceAppleCareCoverage(ctx, deviceID, nil)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) > 0 && resp.Data[0].Attributes != nil {
		a := resp.Data[0].Attributes
		return &AppleCareCoverage{
			AgreementNumber:        a.AgreementNumber,
			ContractCancelDateTime: a.ContractCancelDateTime,
			Description:            a.Description,
			EndDateTime:            a.EndDateTime,
			IsCanceled:             a.IsCanceled,
			IsRenewable:            a.IsRenewable,
			PaymentType:            string(a.PaymentType),
			StartDateTime:          a.StartDateTime,
			Status:                 string(a.Status),
		}, nil
	}
	return nil, nil
}
