package canary

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrPayloadLimit = errors.New("payload exceeds configured ceiling")
	ErrPaymentLimit = errors.New("Fibre payment exceeds configured ceiling")
	ErrEscrow       = errors.New("available Fibre escrow is insufficient")
)

type PutResult struct {
	BlobID string
	TxHash string
	Height uint64
}

// Network is the deliberately small seam around the official pinned clients.
type Network interface {
	Preflight(context.Context, int) (NetworkInfo, error)
	Put(context.Context, []byte) (PutResult, error)
	Download(context.Context, string, uint64) ([]byte, error)
}

type NetworkInfo struct {
	Account             string
	Endpoint            string
	ChainID             string
	ClientVersion       string
	EncodedBytes        int
	PaymentUTIA         uint64
	AvailableEscrowUTIA uint64
}

type Limits struct {
	MaxPayloadBytes int
	MaxPaymentUTIA  uint64
}

type PreflightReport struct {
	NetworkInfo
	PayloadBytes    int
	MaxPayloadBytes int
	MaxPaymentUTIA  uint64
	Vantage         string
}

func Preflight(ctx context.Context, network Network, payloadBytes int, limits Limits, vantage string) (PreflightReport, error) {
	if payloadBytes > limits.MaxPayloadBytes {
		return PreflightReport{}, fmt.Errorf("%w: %d > %d", ErrPayloadLimit, payloadBytes, limits.MaxPayloadBytes)
	}
	info, err := network.Preflight(ctx, payloadBytes)
	if err != nil {
		return PreflightReport{}, fmt.Errorf("network preflight: %w", err)
	}
	report := PreflightReport{NetworkInfo: info, PayloadBytes: payloadBytes, MaxPayloadBytes: limits.MaxPayloadBytes, MaxPaymentUTIA: limits.MaxPaymentUTIA, Vantage: vantage}
	if info.PaymentUTIA > limits.MaxPaymentUTIA {
		return report, fmt.Errorf("%w: %d > %d utia", ErrPaymentLimit, info.PaymentUTIA, limits.MaxPaymentUTIA)
	}
	if info.AvailableEscrowUTIA < info.PaymentUTIA {
		return report, fmt.Errorf("%w: %d < %d utia", ErrEscrow, info.AvailableEscrowUTIA, info.PaymentUTIA)
	}
	return report, nil
}
