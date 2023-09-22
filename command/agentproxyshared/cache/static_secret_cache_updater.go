// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/vault/command/agentproxyshared/cache/cachememdb"

	"github.com/hashicorp/go-hclog"

	"github.com/hashicorp/vault/api"

	"nhooyr.io/websocket"
)

// Example Event:
//{
//  "id": "a3be9fb1-b514-519f-5b25-b6f144a8c1ce",
//  "source": "https://vaultproject.io/",
//  "specversion": "1.0",
//  "type": "*",
//  "data": {
//    "event": {
//      "id": "a3be9fb1-b514-519f-5b25-b6f144a8c1ce",
//      "metadata": {
//        "current_version": "1",
//        "data_path": "secret/data/foo",
//        "modified": "true",
//        "oldest_version": "0",
//        "operation": "data-write",
//        "path": "secret/data/foo"
//      }
//    },
//    "event_type": "kv-v2/data-write",
//    "plugin_info": {
//      "mount_class": "secret",
//      "mount_accessor": "kv_5dc4d18e",
//      "mount_path": "secret/",
//      "plugin": "kv"
//    }
//  },
//  "datacontentype": "application/cloudevents",
//  "time": "2023-09-12T15:19:49.394915-07:00"
//}

const StaticSecretBackoff = 10 * time.Second

// StaticSecretCacheUpdater is a struct that utilizes
// the event system to keep the static secret cache up to date.
type StaticSecretCacheUpdater struct {
	client     *api.Client
	leaseCache *LeaseCache
	logger     hclog.Logger
}

// streamStaticSecretEvents streams static secret events and updates
// the cache when updates are notified. This method will return errors in cases
// of failed updates, malformed events, and other.
// For best results, the caller of this function should retry on error with backoff,
// if it is desired for the cache to always remain up to date.
func (updater *StaticSecretCacheUpdater) streamStaticSecretEvents(ctx context.Context) error {
	var conn *websocket.Conn
	for {
		var err error
		conn, err = updater.openWebSocketConnection(ctx)
		if err != nil {
			updater.logger.Error("error when opening event stream:", err)

			// We backoff in case of Vault downtime etc
			time.Sleep(StaticSecretBackoff)
			continue
		} else {
			break
		}
	}

	defer conn.Close(websocket.StatusNormalClosure, "")

	// before we check for events, update all of our cached
	// kv secrets, in case we missed any events
	// TODO: to be implemented in a future PR

	for {
		_, message, err := conn.Read(ctx)
		if err != nil {
			// The caller of this function should make the decision on if to retry. If it does, then
			// the websocket connection will be retried, and we will check for missed events.
			return fmt.Errorf("error when attempting to read from event stream, reopening websocket: %w", err)
		}
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return fmt.Errorf("error when unmarshaling event, message: %s\nerror: %w", string(message), err)
		}
		data, ok := messageMap["data"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected event format, message: %s\nerror: %w", string(message), err)
		}
		event, ok := data["event"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected event format, message: %s\nerror: %w", string(message), err)
		}
		metadata, ok := event["metadata"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected event format, message: %s\nerror: %w", string(message), err)
		}
		modified, ok := metadata["modified"].(bool)
		if ok && modified {
			path, ok := metadata["path"].(string)
			if !ok {
				return fmt.Errorf("unexpected event format, message: %s\nerror: %w", string(message), err)
			}
			err := updater.updateStaticSecret(ctx, path)
			if err != nil {
				// While we are kind of 'missing' an event this way, re-calling this function will
				// result in the secret remaining up to date.
				return fmt.Errorf("unexpected event format, message: %s\nerror: %w", string(message), err)
			}
		} else {
			// This is an event we're not interested in, ignore it and
			// carry on.
			continue
		}
	}

	return nil
}

func (updater *StaticSecretCacheUpdater) updateStaticSecret(ctx context.Context, path string) error {
	// We clone the client, as we won't be using the same token.
	client, err := updater.client.Clone()
	if err != nil {
		return err
	}

	// TODO: get the index using the path
	// If it doesn't exist, return nil
	// TODO: get the tokens from the lease cache entry, use them to get the secret
	updater.leaseCache.db.Get(cachememdb.IndexNameID, path) // TODO: get the id from the path

	// TODO: get the ID for the
	// updater.leaseCache.db.Get()
	// TODO Update the secret
	// TODO if it's in our cache, then we need to update the index
	// TODO we need a lease cache method for that
	// lc.updateStaticSecret(secret, path)

	secret, err := client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		// This isn't good, maybe Vault is down or similar, but we cannot
		// simply error out here or ignore this event.
		// TODO decide what to do
	}

	updater.logger.Info("Logging secret for debugging purposes", "secret", secret)
	// TODO Update the secret

	return nil
}

func (updater *StaticSecretCacheUpdater) openWebSocketConnection(ctx context.Context) (*websocket.Conn, error) {
	wsLocation := fmt.Sprintf("ws://%s/v1/sys/events/subscribe/kv*?json=true", updater.client.Address())
	updater.client.AddHeader("X-Vault-Token", updater.client.Token())
	updater.client.AddHeader("X-Vault-Namespace", updater.client.Namespace())
	conn, _, err := websocket.Dial(ctx, wsLocation, &websocket.DialOptions{
		HTTPClient: updater.client.CloneConfig().HttpClient,
		HTTPHeader: updater.client.Headers(),
	})
	if err != nil {
		return nil, fmt.Errorf("error returned when opening event stream web socket, ensure auto-auth token"+
			" has correct permissions and Vault is version 1.16 or above: %w", err)
	}
	return conn, nil
}
