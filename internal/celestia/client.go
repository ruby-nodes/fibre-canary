// Package celestia adapts the pinned official Celestia Fibre and transaction
// clients to the canary workflow.
package celestia

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime/debug"

	"example.com/fibre-canary/internal/buildinfo"
	"example.com/fibre-canary/internal/canary"
	"example.com/fibre-canary/internal/config"
	"github.com/celestiaorg/celestia-app/v10/app"
	appencoding "github.com/celestiaorg/celestia-app/v10/app/encoding"
	"github.com/celestiaorg/celestia-app/v10/fibre"
	"github.com/celestiaorg/celestia-app/v10/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v10/pkg/user"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	"github.com/celestiaorg/go-square/v4/share"
	tmservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	cfg       config.Config
	conn      *grpc.ClientConn
	fibre     *fibre.Client
	tx        *user.TxClient
	namespace share.Namespace
	account   string
	escrow    uint64
	cancel    context.CancelFunc
}

// New constructs and starts the official pinned Fibre and transaction clients.
// input is used by keyring backends that prompt interactively.
func New(ctx context.Context, cfg config.Config, input io.Reader) (*Client, error) {
	if err := verifyPinnedModule(); err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(cfg.AppGRPCAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create app gRPC client: %w", err)
	}
	fail := func(err error) (*Client, error) { _ = conn.Close(); return nil, err }

	node, err := tmservice.NewServiceClient(conn).GetNodeInfo(ctx, &tmservice.GetNodeInfoRequest{})
	if err != nil {
		return fail(fmt.Errorf("query node identity: %w", err))
	}
	if got := node.DefaultNodeInfo.Network; got != cfg.ChainID {
		return fail(fmt.Errorf("chain identity mismatch: endpoint reports %q, expected %q", got, cfg.ChainID))
	}

	enc := appencoding.MakeConfig(app.ModuleEncodingRegisters...)
	keys, err := keyring.New(app.Name, cfg.KeyringBackend, cfg.KeyringDir, input, enc.Codec)
	if err != nil {
		return fail(fmt.Errorf("open keyring: %w", err))
	}
	record, err := keys.Key(cfg.KeyName)
	if err != nil {
		return fail(fmt.Errorf("load public key identity %q: %w", cfg.KeyName, err))
	}
	address, err := record.GetAddress()
	if err != nil {
		return fail(fmt.Errorf("derive account address: %w", err))
	}
	account := address.String()

	escrowResp, err := fibretypes.NewQueryClient(conn).EscrowAccount(ctx, &fibretypes.QueryEscrowAccountRequest{Signer: account})
	if err != nil {
		return fail(fmt.Errorf("query Fibre escrow: %w", err))
	}
	var escrow uint64
	if escrowResp.Found {
		coin := escrowResp.EscrowAccount.AvailableBalance
		if coin.Denom != appconsts.BondDenom || coin.Amount.IsNegative() || !coin.Amount.IsUint64() {
			return fail(fmt.Errorf("unsupported Fibre escrow amount %s", coin.String()))
		}
		escrow = coin.Amount.Uint64()
	}

	fibreCfg := fibre.DefaultClientConfig()
	fibreCfg.StateAddress = cfg.AppGRPCAddress
	fibreCfg.DefaultKeyName = cfg.KeyName
	fibreCfg.RPCTimeout = cfg.Timeout()
	fibreCfg.Escrow.AutoFund = false
	fibreClient, err := fibre.NewClient(keys, fibreCfg)
	if err != nil {
		return fail(fmt.Errorf("create Fibre client: %w", err))
	}
	if err := fibreClient.Start(ctx); err != nil {
		return fail(fmt.Errorf("start Fibre client: %w", err))
	}
	if fibreClient.ChainID() != cfg.ChainID {
		_ = fibreClient.Stop(context.Background())
		return fail(fmt.Errorf("Fibre chain identity mismatch: %q", fibreClient.ChainID()))
	}

	clientCtx, cancel := context.WithCancel(ctx)
	txClient, err := user.SetupTxClient(clientCtx, keys, conn, enc, user.WithDefaultAccount(cfg.KeyName))
	if err != nil {
		cancel()
		_ = fibreClient.Stop(context.Background())
		return fail(fmt.Errorf("create transaction client: %w", err))
	}
	nsBytes, _ := hex.DecodeString(cfg.NamespaceHex)
	ns, err := share.NewV0Namespace(nsBytes[18:])
	if err != nil {
		cancel()
		_ = fibreClient.Stop(context.Background())
		return fail(fmt.Errorf("construct namespace: %w", err))
	}
	return &Client{cfg: cfg, conn: conn, fibre: fibreClient, tx: txClient, namespace: ns, account: account, escrow: escrow, cancel: cancel}, nil
}

func (c *Client) Preflight(_ context.Context, payloadBytes int) (canary.NetworkInfo, error) {
	uploadSize := fibre.DefaultBlobConfigV0().UploadSize(payloadBytes)
	payment := fibretypes.PaymentAmount(uint32(uploadSize))
	if payment.Denom != appconsts.BondDenom || !payment.Amount.IsUint64() {
		return canary.NetworkInfo{}, fmt.Errorf("unsupported Fibre payment %s", payment.String())
	}
	return canary.NetworkInfo{
		Account: c.account, Endpoint: c.cfg.AppGRPCAddress, ChainID: c.cfg.ChainID,
		ClientVersion: buildinfo.CelestiaAppVersion, EncodedBytes: uploadSize,
		PaymentUTIA: payment.Amount.Uint64(), AvailableEscrowUTIA: c.escrow,
	}, nil
}

func (c *Client) Put(ctx context.Context, payload []byte) (canary.PutResult, error) {
	result, err := fibre.Put(ctx, c.fibre, c.tx, c.namespace, payload)
	if err != nil {
		return canary.PutResult{}, err
	}
	return canary.PutResult{BlobID: result.BlobID.String(), TxHash: result.TxHash, Height: result.Height}, nil
}

func (c *Client) Download(ctx context.Context, blobID string, height uint64) ([]byte, error) {
	id, err := fibre.BlobIDFromString(blobID)
	if err != nil {
		return nil, fmt.Errorf("decode blob ID: %w", err)
	}
	blob, err := c.fibre.Download(ctx, id, fibre.WithHeight(height))
	if err != nil {
		return nil, fmt.Errorf("download blob: %w", err)
	}
	defer blob.Free()
	return append([]byte(nil), blob.Data()...), nil
}

func (c *Client) Close(ctx context.Context) error {
	c.cancel()
	err := c.fibre.Stop(ctx)
	return errors.Join(err, c.conn.Close())
}

func verifyPinnedModule() error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("cannot verify compiled Celestia dependency")
	}
	for _, dep := range info.Deps {
		if dep.Path == buildinfo.CelestiaAppModule {
			version := dep.Version
			if dep.Replace != nil {
				version = dep.Replace.Version
			}
			if version != buildinfo.CelestiaAppVersion {
				return fmt.Errorf("unsupported compiled Celestia version %q; expected %q", version, buildinfo.CelestiaAppVersion)
			}
			return nil
		}
	}
	return fmt.Errorf("compiled binary does not contain %s", buildinfo.CelestiaAppModule)
}
