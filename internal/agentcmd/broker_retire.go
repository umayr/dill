package agentcmd

import (
	"bytes"
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"time"
)

func (c brokerCLI) retire(ctx context.Context, path string, r brokersecret.Retirement) (brokersecret.Review, error) {
	raw, err := brokersecret.EncodeRetirement(r)
	if err != nil {
		return brokersecret.Review{}, err
	}
	reply, err := c.call(ctx, "broker-retirement-prepare", path, r.Scope, bytes.NewReader(raw))
	if err != nil || reply.Prepared == nil || reply.Prepared.Scope != r.Scope || reply.Prepared.Active || !brokersecret.ValidHandle(reply.Prepared.Handle) || !reply.Prepared.ExpiresAt.After(time.Now()) || reply.Prepared.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		return brokersecret.Review{}, brokersecret.ErrUnavailable
	}
	return *reply.Prepared, nil
}
