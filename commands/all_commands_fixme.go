package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"

	ma "gx/ipfs/QmWWQ2Txc2c6tqjsBpzg5Ar652cHPGNsQQp2SejkNmkUMb/go-multiaddr"
	//peer "gx/ipfs/QmWNY7dV54ZDYmTA1ykVdwNCqC11mpU4zSUp6XDpLTH9eG/go-libp2p-peer"
	swarm "gx/ipfs/QmSD9fajyipwNQw3Hza2k2ifcBfbhGoC1ZHHgQBy4yqU8d/go-libp2p-swarm"
	ipfsaddr "gx/ipfs/QmWto9a6kfznUoW9owbLoBiLUMgRvgsVHRKFzs8zzzKYwp/go-ipfs-addr"
	crypto "gx/ipfs/QmaPbCnUMBohSGo3KnxEa2bHqyJVVeEEcwtqJAYxerieBo/go-libp2p-crypto"
	"gx/ipfs/QmcZfnkapfECQGcLZaf9B79NRg7cRa9EnZh4LSbkCzwNvY/go-cid"
	pstore "gx/ipfs/QmeZVQzUrXqaszo24DAoHfGzcmCptN9JyngLkGAiEfk2x7/go-libp2p-peerstore"

	contract "github.com/filecoin-project/go-filecoin/contract"
	core "github.com/filecoin-project/go-filecoin/core"
	libp2p "github.com/filecoin-project/go-filecoin/libp2p"
	types "github.com/filecoin-project/go-filecoin/types"

	"gx/ipfs/Qma2TkMxcFLVGkYECTo4hrQohBYPx7uhpYL9EejEi8y3Nm/go-libp2p-floodsub"

	ds "gx/ipfs/QmdHG8MAuARdGHxx4rPQASLcvhz24fzjSQq7AJRAQEorq5/go-datastore"
	dssync "gx/ipfs/QmdHG8MAuARdGHxx4rPQASLcvhz24fzjSQq7AJRAQEorq5/go-datastore/sync"

	bstore "github.com/ipfs/go-ipfs/blocks/blockstore"
	bserv "github.com/ipfs/go-ipfs/blockservice"
	bitswap "github.com/ipfs/go-ipfs/exchange/bitswap"
	bsnet "github.com/ipfs/go-ipfs/exchange/bitswap/network"
	dag "github.com/ipfs/go-ipfs/merkledag"
	path "github.com/ipfs/go-ipfs/path"
	none "github.com/ipfs/go-ipfs/routing/none"

	cmds "gx/ipfs/Qmc5paX4ECBARnAKkcAmUYHBGor228Tkfxeya3Nu2KRL46/go-ipfs-cmds"
	cmdhttp "gx/ipfs/Qmc5paX4ECBARnAKkcAmUYHBGor228Tkfxeya3Nu2KRL46/go-ipfs-cmds/http"
	cmdkit "gx/ipfs/QmceUdzxkimdYsgtX733uNgzf1DLHyBKN6ehGSp85ayppM/go-ipfs-cmdkit"
)

var RootCmd = &cmds.Command{
	Options: []cmdkit.Option{
		cmdkit.StringOption("api", "set the api port to use").WithDefault(":3453"),
		cmds.OptionEncodingType,
	},
	Subcommands: make(map[string]*cmds.Command),
}

var rootSubcommands = map[string]*cmds.Command{
	"daemon":  DaemonCmd,
	"addrs":   AddrsCmd,
	"bitswap": BitswapCmd,
	"dag":     DagCmd,
	"wallet":  WalletCmd,
	"order":   OrderCmd,
	"miner":   MinerCmd,
	"swarm":   SwarmCmd,
	"id":      IdCmd,
}

func init() {
	for k, v := range rootSubcommands {
		RootCmd.Subcommands[k] = v
	}
}

var DaemonCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "run the filecoin daemon",
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("bootstrap", false, true, "nodes to bootstrap to"),
	},
	Run: daemonRun,
}

func daemonRun(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
	api := req.Options["api"].(string)

	hsh := fnv.New64()
	hsh.Write([]byte(api))
	seed := hsh.Sum64()

	r := rand.New(rand.NewSource(int64(seed)))
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
	if err != nil {
		panic(err)
	}

	p2pcfg := libp2p.DefaultConfig()
	p2pcfg.PeerKey = priv
	laddr, err := ma.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 1000+(seed%20000)))
	if err != nil {
		panic(err)
	}

	p2pcfg.ListenAddrs = []ma.Multiaddr{laddr}

	// set up networking
	h, err := libp2p.Construct(context.Background(), p2pcfg)
	if err != nil {
		panic(err)
	}

	fsub := floodsub.NewFloodSub(context.Background(), h)

	// set up storage (a bit more complicated than it realistically needs to be right now)
	bs := bstore.NewBlockstore(dssync.MutexWrap(ds.NewMapDatastore()))
	nilr, _ := none.ConstructNilRouting(nil, nil, nil)
	bsnet := bsnet.NewFromIpfsHost(h, nilr)
	bswap := bitswap.New(context.Background(), h.ID(), bsnet, bs, true)
	bserv := bserv.New(bs, bswap)
	dag := dag.NewDAGService(bserv)

	// TODO: work on what parameters we pass to the filecoin node
	fcn, err := core.NewFilecoinNode(h, fsub, dag, bserv, bswap.(*bitswap.Bitswap))
	if err != nil {
		panic(err)
	}

	if len(req.Arguments) > 0 {
		a, err := ipfsaddr.ParseString(req.Arguments[0])
		if err != nil {
			panic(err)
		}
		err = h.Connect(context.Background(), pstore.PeerInfo{
			ID:    a.ID(),
			Addrs: []ma.Multiaddr{a.Transport()},
		})
		if err != nil {
			panic(err)
		}
		fmt.Println("Connected to other peer!")
	}

	for _, a := range h.Addrs() {
		fmt.Println(a.String() + "/ipfs/" + h.ID().Pretty())
	}

	if err := writeDaemonLock(); err != nil {
		panic(err)
	}

	servenv := &CommandEnv{
		ctx:  context.Background(),
		Node: fcn,
	}

	cfg := cmdhttp.NewServerConfig()
	cfg.APIPath = "/api"

	handler := cmdhttp.NewHandler(servenv, RootCmd, cfg)

	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt)

	go func() {
		panic(http.ListenAndServe(api, handler))
	}()

	<-ch
	removeDaemonLock()
}

type CommandEnv struct {
	ctx  context.Context
	Node *core.FilecoinNode
}

func (ce *CommandEnv) Context() context.Context {
	return ce.ctx
}

func GetNode(env cmds.Environment) *core.FilecoinNode {
	ce := env.(*CommandEnv)
	return ce.Node
}

var AddrsCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"new":    AddrsNewCmd,
		"list":   AddrsListCmd,
		"lookup": AddrsLookupCmd,
	},
}

var AddrsNewCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		naddr := core.CreateNewAddress()
		fcn.Addresses = append(fcn.Addresses, naddr)
		re.Emit(naddr)
	},
	Type: types.Address(""),
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			a, ok := v.(*types.Address)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			_, err := fmt.Fprintln(w, a.String())
			return err
		}),
	},
}

var AddrsListCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		re.Emit(fcn.Addresses)
	},
	Type: []types.Address{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			addrs, ok := v.(*[]types.Address)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			for _, a := range *addrs {
				_, err := fmt.Fprintln(w, a.String())
				if err != nil {
					return err
				}
			}
			return nil
		}),
	},
}

var AddrsLookupCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("address", true, false, "address to find peerID for"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		address, err := types.ParseAddress(req.Arguments[0])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		v, err := fcn.Lookup.Lookup(address)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
		re.Emit(v.Pretty())
	},
	Type: string(""),
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			pid, ok := v.(string)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			_, err := fmt.Fprintln(w, pid)
			if err != nil {
				return err
			}
			return nil
		}),
	},
}

var BitswapCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"wantlist": BitswapWantlistCmd,
	},
}

var BitswapWantlistCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		re.Emit(fcn.Bitswap.GetWantlist())
	},
	Type: []*cid.Cid{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			wants, ok := v.(*[]cid.Cid)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			for _, want := range *wants {
				_, err := fmt.Fprintln(w, want.String())
				if err != nil {
					return err
				}
			}
			return nil
		}),
	},
}

var DagCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"get": DagGetCmd,
	},
}

var DagGetCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("object", true, false, "ref of node to get"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		res := path.NewBasicResolver(fcn.DAG)

		p, err := path.ParsePath(req.Arguments[0])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		nd, err := res.ResolvePath(req.Context, p)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		re.Emit(nd)
	},
}

var WalletCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"send":    WalletSendCmd,
		"balance": WalletGetBalanceCmd,
	},
}

var WalletGetBalanceCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("account", false, false, "account to get balance of"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		addr := fcn.Addresses[0]
		if len(req.Arguments) > 0 {
			a, err := types.ParseAddress(req.Arguments[0])
			if err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}
			addr = a
		}

		stroot := fcn.StateMgr.GetStateRoot()
		act, err := stroot.GetActor(req.Context, contract.FilecoinContractAddr)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		ct, err := stroot.GetContract(req.Context, act.Code)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		cst, err := stroot.LoadContractState(req.Context, act.Memory)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		cctx := &contract.CallContext{Ctx: req.Context, ContractState: cst}
		val, err := ct.Call(cctx, "getBalance", []interface{}{addr})
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		re.Emit(val)
	},
	Type: big.Int{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			val, ok := v.(*big.Int)
			if !ok {
				return fmt.Errorf("got unexpected type: %T", v)
			}
			fmt.Fprintln(w, val.String())
			return nil
		}),
	},
}

// TODO: this command should exist in some form, but its really specialized.
// The issue is that its really 'call transfer on the filecoin token contract
// and send tokens from our default account to a given actor'.
var WalletSendCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("value", true, false, "amount to send"),
		cmdkit.StringArg("to", true, false, "actor to send transaction to"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		amount, ok := big.NewInt(0).SetString(req.Arguments[0], 10)
		if !ok {
			re.SetError("failed to parse amount", cmdkit.ErrNormal)
			return
		}
		toaddr, err := types.ParseAddress(req.Arguments[1])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		from := fcn.Addresses[0]

		nonce, err := fcn.StateMgr.GetStateRoot().NonceForActor(req.Context, from)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		tx := &types.Transaction{
			From:   from,
			To:     contract.FilecoinContractAddr,
			Nonce:  nonce,
			Method: "transfer",
			Params: []interface{}{toaddr, amount},
		}

		fcn.SendNewTransaction(tx)
	},
}

var MinerCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"new": MinerNewCmd,
	},
}

var MinerNewCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("pledge-size", true, false, "size of pledge to create miner with"),
	},
	Type: types.Address(""),
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		pledge, ok := big.NewInt(0).SetString(req.Arguments[0], 10)
		if !ok {
			re.SetError("failed to parse pledge as number", cmdkit.ErrNormal)
			return
		}

		from := fcn.Addresses[0]

		nonce, err := fcn.StateMgr.StateRoot.NonceForActor(req.Context, from)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		tx := &types.Transaction{
			From:   fcn.Addresses[0],
			To:     contract.StorageContractAddress,
			Nonce:  nonce,
			Method: "createMiner",
			Params: []interface{}{pledge},
		}

		res, err := fcn.SendNewTransactionAndWait(req.Context, tx)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		if !res.Receipt.Success {
			re.SetError("miner creation failed", cmdkit.ErrNormal)
			return
		}

		resStr, ok := res.Receipt.Result.(types.Address)
		if !ok {
			// TODO: this returns a string instead of an address if the transaction was mined by someone else. Yay types...
			re.SetError("createMiner call didn't return an address", cmdkit.ErrNormal)
			return
		}

		re.Emit(resStr)
	},
}

var OrderCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"bid":  OrderBidCmd,
		"ask":  OrderAskCmd,
		"deal": OrderDealCmd,
	},
}

var OrderBidCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"add":  OrderBidAddCmd,
		"list": OrderBidListCmd,
	},
}

var OrderBidAddCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("price", true, false, "price for bid"),
		cmdkit.StringArg("size", true, false, "size of bid"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		price, err := strconv.Atoi(req.Arguments[0])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		size, err := strconv.Atoi(req.Arguments[1])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		from := fcn.Addresses[0]

		nonce, err := fcn.StateMgr.StateRoot.NonceForActor(req.Context, from)

		tx := &types.Transaction{
			From:   fcn.Addresses[0],
			To:     contract.StorageContractAddress,
			Nonce:  nonce,
			Method: "addBid",
			Params: []interface{}{uint64(price), uint64(size)},
		}

		if err := fcn.SendNewTransaction(tx); err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
	},
}

var OrderBidListCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		bids, err := listBids(req.Context, fcn)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
		re.Emit(bids)
	},
	Type: []*contract.Bid{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			bids, ok := v.(*[]*contract.Bid)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			for _, b := range *bids {
				fmt.Fprintln(w, b.Owner, b.Price, b.Size, b.Collateral)
			}
			return nil
		}),
	},
}
var OrderAskCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"add":  OrderAskAddCmd,
		"list": OrderAskListCmd,
	},
}

var OrderAskAddCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("miner", true, false, "miner to create ask on"),
		cmdkit.StringArg("price", true, false, "price per byte being asked"),
		cmdkit.StringArg("size", true, false, "total size being offered"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		miner, err := types.ParseAddress(req.Arguments[0])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		price, err := strconv.Atoi(req.Arguments[1])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		size, err := strconv.Atoi(req.Arguments[2])
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		from := fcn.Addresses[0]

		nonce, err := fcn.StateMgr.StateRoot.NonceForActor(req.Context, from)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		tx := &types.Transaction{
			From:   fcn.Addresses[0],
			To:     contract.StorageContractAddress,
			Nonce:  nonce,
			Method: "addAsk",
			Params: []interface{}{miner, int64(price), uint64(size)},
		}

		if err := fcn.SendNewTransaction(tx); err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
	},
}

var OrderAskListCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		asks, err := listAsks(req.Context, fcn)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
		re.Emit(asks)
	},
	Type: []*contract.Ask{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			asks, ok := v.(*[]*contract.Ask)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			for _, a := range *asks {
				fmt.Fprintf(w, "%s\t%d\t%s\t%d\n", a.MinerID, a.Size, a.Price, a.Expiry)
			}
			return nil
		}),
	},
}

func listAsks(ctx context.Context, fcn *core.FilecoinNode) ([]*contract.Ask, error) {
	stroot := fcn.StateMgr.GetStateRoot()
	sact, err := stroot.GetActor(ctx, contract.StorageContractAddress)
	if err != nil {
		return nil, err
	}

	c, err := stroot.GetContract(ctx, sact.Code)
	if err != nil {
		return nil, err
	}

	cst, err := stroot.LoadContractState(ctx, sact.Memory)
	if err != nil {
		return nil, err
	}

	sc, ok := c.(*contract.StorageContract)
	if !ok {
		return nil, fmt.Errorf("was not actually a storage contract somehow")
	}

	cctx := &contract.CallContext{ContractState: cst, Ctx: ctx}

	return sc.ListAsks(cctx)
}

func listBids(ctx context.Context, fcn *core.FilecoinNode) ([]*contract.Bid, error) {
	sc, cst, err := fcn.LoadStorageContract(ctx)
	if err != nil {
		return nil, err
	}

	cctx := &contract.CallContext{ContractState: cst, Ctx: ctx}
	return sc.ListBids(cctx)
}

var OrderDealCmd = &cmds.Command{
	Subcommands: map[string]*cmds.Command{
		"make": OrderDealMakeCmd,
		"list": OrderDealListCmd,
	},
}

var OrderDealMakeCmd = &cmds.Command{
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("ask", true, false, "id of ask for deal"),
		cmdkit.StringArg("bid", true, false, "id of bid for deal"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		ask, err := strconv.ParseUint(req.Arguments[0], 10, 64)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		bid, err := strconv.ParseUint(req.Arguments[1], 10, 64)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		// TODO:
		// making random data hunk here. Need to let the user specify their data at some point
		buf := make([]byte, 128)
		rand.Read(buf)
		nd := dag.NewRawNode(buf)
		if _, err := fcn.DAG.Add(nd); err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		txcid, err := core.ClientMakeDeal(req.Context, fcn, ask, bid, nd.Cid())
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		fmt.Println("TRANSACTION FOR DEAL: ", txcid)
	},
	Type: cid.Cid{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			txh, ok := v.(*cid.Cid)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			fmt.Fprintln(w, txh.String())
			return nil
		}),
	},
}

var OrderDealListCmd = &cmds.Command{
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)
		sc, st, err := fcn.LoadStorageContract(req.Context)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		cctx := &contract.CallContext{Ctx: req.Context, ContractState: st}
		deals, err := sc.ListDeals(cctx)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		re.Emit(deals)
	},
	Type: []*contract.Deal{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			deals, ok := v.([]*contract.Deal)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			for _, d := range deals {
				fmt.Fprintf(w, "%d %d %s", d.Ask, d.Bid, d.DataRef)
			}

			return nil
		}),
	},
}

type idOutput struct {
	Addresses       []string
	ID              string
	AgentVersion    string
	ProtocolVersion string
	PublicKey       string
}

var IdCmd = &cmds.Command{
	Options: []cmdkit.Option{
		// TODO: ideally copy this from the `ipfs id` command
		cmdkit.StringOption("format", "f", "specify an output format"),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		fcn := GetNode(env)

		var out idOutput
		for _, a := range fcn.Host.Addrs() {
			out.Addresses = append(out.Addresses, fmt.Sprintf("%s/ipfs/%s", a, fcn.Host.ID().Pretty()))
		}
		out.ID = fcn.Host.ID().Pretty()

		re.Emit(&out)
	},
	Type: idOutput{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			val, ok := v.(*idOutput)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			format, found := req.Options["format"].(string)
			if found {
				output := format
				output = strings.Replace(output, "<id>", val.ID, -1)
				output = strings.Replace(output, "<aver>", val.AgentVersion, -1)
				output = strings.Replace(output, "<pver>", val.ProtocolVersion, -1)
				output = strings.Replace(output, "<pubkey>", val.PublicKey, -1)
				output = strings.Replace(output, "<addrs>", strings.Join(val.Addresses, "\n"), -1)
				output = strings.Replace(output, "\\n", "\n", -1)
				output = strings.Replace(output, "\\t", "\t", -1)
				_, err := fmt.Fprint(w, output)
				return err
			} else {

				marshaled, err := json.MarshalIndent(val, "", "\t")
				if err != nil {
					return err
				}
				marshaled = append(marshaled, byte('\n'))

				_, err = w.Write(marshaled)
				return err
			}
		}),
	},
}

var SwarmCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Interact with the swarm.",
		ShortDescription: `
'go-filecoin swarm' is a tool to manipulate the libp2p swarm. The swarm is the
component that opens, listens for, and maintains connections to other
libp2p peers on the internet.
`,
	},
	Subcommands: map[string]*cmds.Command{
		//"addrs":      swarmAddrsCmd,
		"connect": swarmConnectCmd,
		//"disconnect": swarmDisconnectCmd,
		//"filters":    swarmFiltersCmd,
		//"peers": swarmPeersCmd,
	},
}

// COPIED FROM go-ipfs core/commands/swarm.go

// parseAddresses is a function that takes in a slice of string peer addresses
// (multiaddr + peerid) and returns slices of multiaddrs and peerids.
func parseAddresses(addrs []string) (iaddrs []ipfsaddr.IPFSAddr, err error) {
	iaddrs = make([]ipfsaddr.IPFSAddr, len(addrs))
	for i, saddr := range addrs {
		iaddrs[i], err = ipfsaddr.ParseString(saddr)
		if err != nil {
			return nil, cmds.ClientError("invalid peer address: " + err.Error())
		}
	}
	return
}

// peersWithAddresses is a function that takes in a slice of string peer addresses
// (multiaddr + peerid) and returns a slice of properly constructed peers
func peersWithAddresses(addrs []string) (pis []pstore.PeerInfo, err error) {
	iaddrs, err := parseAddresses(addrs)
	if err != nil {
		return nil, err
	}

	for _, iaddr := range iaddrs {
		pis = append(pis, pstore.PeerInfo{
			ID:    iaddr.ID(),
			Addrs: []ma.Multiaddr{iaddr.Transport()},
		})
	}
	return pis, nil
}

type connectResult struct {
	Peer    string
	Success bool
}

var swarmConnectCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Open connection to a given address.",
		ShortDescription: `
'go-filecoin swarm connect' opens a new direct connection to a peer address.

The address format is a multiaddr:

go-filecoin swarm connect /ip4/104.131.131.82/tcp/4001/ipfs/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ
`,
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("address", true, true, "Address of peer to connect to.").EnableStdin(),
	},
	Run: func(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) {
		ctx := req.Context

		n := GetNode(env)

		addrs := req.Arguments

		snet, ok := n.Host.Network().(*swarm.Network)
		if !ok {
			re.SetError("peerhost network was not swarm", cmdkit.ErrNormal)
			return
		}

		swrm := snet.Swarm()

		pis, err := peersWithAddresses(addrs)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		output := make([]connectResult, len(pis))
		for i, pi := range pis {
			swrm.Backoff().Clear(pi.ID)

			output[i].Peer = pi.ID.Pretty()

			err := n.Host.Connect(ctx, pi)
			if err != nil {
				re.SetError(fmt.Errorf("%s failure: %s", output[i], err), cmdkit.ErrNormal)
				return
			}
		}

		re.Emit(output)
	},
	Type: []connectResult{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req *cmds.Request, w io.Writer, v interface{}) error {
			res, ok := v.(*[]connectResult)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}
			for _, a := range *res {
				fmt.Fprintf(w, "connect %s success\n", a.Peer)
			}
			return nil
		}),
	},
}
