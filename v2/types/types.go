// Package types defines the essential types of the Sia blockchain.
package types

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"sync"
	"time"

	"github.com/hdevalence/ed25519consensus"
	"golang.org/x/crypto/blake2b"
	"lukechampine.com/frand"
)

// EphemeralLeafIndex is used as the LeafIndex of StateElements that are created
// and spent within the same block. Such elements do not require a proof of
// existence. They are, however, assigned a proper index and are incorporated
// into the state accumulator when the block is processed.
const EphemeralLeafIndex = math.MaxUint64

// MaxRevisionNumber is used to finalize a FileContract. When a contract's
// RevisionNumber is set to this value, no further revisions are possible. This
// allows contracts to be resolved "early" in some cases; see
// FileContractResolution.
const MaxRevisionNumber = math.MaxUint64

// A Hash256 is a generic 256-bit cryptographic hash.
type Hash256 [32]byte

// An Address is the hash of a public key.
type Address Hash256

// VoidAddress is an address whose signing key does not exist. Sending coins to
// this address ensures that they will never be recoverable by anyone.
var VoidAddress Address

// A BlockID uniquely identifies a block.
type BlockID Hash256

// MeetsTarget returns true if bid is not greater than t.
func (bid BlockID) MeetsTarget(t BlockID) bool {
	return bytes.Compare(bid[:], t[:]) <= 0
}

// A TransactionID uniquely identifies a transaction.
type TransactionID Hash256

// A ChainIndex pairs a block's height with its ID.
type ChainIndex struct {
	Height uint64
	ID     BlockID
}

// A PublicKey is an Ed25519 public key.
type PublicKey [32]byte

// A PrivateKey is an Ed25519 private key.
type PrivateKey []byte

// PublicKey returns the PublicKey corresponding to priv.
func (priv PrivateKey) PublicKey() (pk PublicKey) {
	copy(pk[:], priv[32:])
	return
}

// NewPrivateKeyFromSeed calculates a private key from a seed.
func NewPrivateKeyFromSeed(seed []byte) PrivateKey {
	return PrivateKey(ed25519.NewKeyFromSeed(seed))
}

// GeneratePrivateKey creates a new private key from a secure entropy source.
func GeneratePrivateKey() PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	frand.Read(seed)
	pk := NewPrivateKeyFromSeed(seed)
	for i := range seed {
		seed[i] = 0
	}
	return pk
}

// A Signature is an Ed25519 signature.
type Signature [64]byte

// SignHash signs h with priv, producing a Signature.
func (priv PrivateKey) SignHash(h Hash256) (s Signature) {
	copy(s[:], ed25519.Sign(ed25519.PrivateKey(priv), h[:]))
	return
}

// VerifyHash verifies that s is a valid signature of h by pk.
func (pk PublicKey) VerifyHash(h Hash256, s Signature) bool {
	return ed25519consensus.Verify(pk[:], h[:], s[:])
}

// A SiacoinOutput is the recipient of some of the siacoins spent in a
// transaction.
type SiacoinOutput struct {
	Value   Currency
	Address Address
}

// A SiafundOutput is the recipient of some of the siafunds spent in a
// transaction.
type SiafundOutput struct {
	Value   uint64
	Address Address
}

// A FileContract is a storage agreement between a renter and a host. It
// consists of a bidirectional payment channel that resolves as either "valid"
// or "missed" depending on whether a valid StorageProof is submitted for the
// contract.
type FileContract struct {
	Filesize        uint64
	FileMerkleRoot  Hash256
	WindowStart     uint64
	WindowEnd       uint64
	RenterOutput    SiacoinOutput
	HostOutput      SiacoinOutput
	MissedHostValue Currency
	TotalCollateral Currency
	RenterPublicKey PublicKey
	HostPublicKey   PublicKey
	RevisionNumber  uint64

	// signatures cover above fields
	RenterSignature Signature
	HostSignature   Signature
}

// MissedHostOutput returns the host output that will be created if the contract
// resolves missed.
func (fc FileContract) MissedHostOutput() SiacoinOutput {
	return SiacoinOutput{
		Value:   fc.MissedHostValue,
		Address: fc.HostOutput.Address,
	}
}

// A SiacoinInput spends an unspent SiacoinElement in the state accumulator by
// revealing its public key and signing the transaction.
type SiacoinInput struct {
	Parent      SiacoinElement
	SpendPolicy SpendPolicy
	Signatures  []Signature
}

// A SiafundInput spends an unspent SiafundElement in the state accumulator by
// revealing its public key and signing the transaction. Inputs also include a
// ClaimAddress, specifying the recipient of the siacoins that were earned by
// the SiafundElement.
type SiafundInput struct {
	Parent       SiafundElement
	ClaimAddress Address
	SpendPolicy  SpendPolicy
	Signatures   []Signature
}

// A FileContractRevision updates the state of an existing file contract.
type FileContractRevision struct {
	Parent   FileContractElement
	Revision FileContract
}

// A FileContractResolution closes a file contract's payment channel. There are
// four ways a contract can be resolved:
//
// 1) The renter and host can renew the contract. The old contract is finalized,
// and a portion of its funds are "rolled over" into a new contract.
//
// 2) The host can submit a valid storage proof within the contract's proof
// window. This is considered a "valid" resolution.
//
// 3) The renter and host can sign a final contract revision (a "finalization"),
// setting the contract's revision number to its maximum legal value. A
// finalization can be submitted at any time prior to the contract's WindowEnd.
// This is considered a "valid" resolution.
//
// 4) After the proof window has expired, anyone can submit an empty resolution
// with no storage proof or finalization. This is considered a "missed"
// resolution.
type FileContractResolution struct {
	Parent       FileContractElement
	Renewal      FileContractRenewal
	StorageProof StorageProof
	Finalization FileContract
}

// HasRenewal returns true if the resolution contains a renewal.
func (fcr *FileContractResolution) HasRenewal() bool {
	return fcr.Renewal != (FileContractRenewal{})
}

// HasStorageProof returns true if the resolution contains a storage proof.
func (fcr *FileContractResolution) HasStorageProof() bool {
	sp := &fcr.StorageProof
	return sp.WindowStart != (ChainIndex{}) || len(sp.WindowProof) > 0 ||
		sp.Leaf != ([64]byte{}) || len(sp.Proof) > 0
}

// HasFinalization returns true if the resolution contains a finalization.
func (fcr *FileContractResolution) HasFinalization() bool {
	return fcr.Finalization != (FileContract{})
}

// A FileContractRenewal renews a file contract.
type FileContractRenewal struct {
	FinalRevision   FileContract
	InitialRevision FileContract
	RenterRollover  Currency
	HostRollover    Currency

	// signatures cover above fields
	RenterSignature Signature
	HostSignature   Signature
}

// A StorageProof asserts the presence of a randomly-selected leaf within the
// Merkle tree of a FileContract's data.
type StorageProof struct {
	// Selecting the leaf requires a source of unpredictable entropy; we use the
	// ID of the block at the start of the proof window. The StorageProof
	// includes this ID, and asserts its presence in the chain via a separate
	// Merkle proof.
	//
	// For convenience, WindowStart is a ChainIndex rather than a BlockID.
	// Consequently, WindowStart.Height MUST match the WindowStart field of the
	// contract's final revision; otherwise, the prover could use any
	// WindowStart, giving them control over the leaf index.
	WindowStart ChainIndex
	WindowProof []Hash256

	// The leaf is always 64 bytes, extended with zeros if necessary.
	Leaf  [64]byte
	Proof []Hash256
}

// An ElementID uniquely identifies a StateElement.
type ElementID struct {
	Source Hash256 // BlockID or TransactionID
	Index  uint64
}

// A StateElement is a generic element within the state accumulator.
type StateElement struct {
	ID          ElementID
	LeafIndex   uint64
	MerkleProof []Hash256
}

// A SiacoinElement is a volume of siacoins that is created and spent as an
// atomic unit.
type SiacoinElement struct {
	StateElement
	SiacoinOutput
	MaturityHeight uint64
}

// A SiafundElement is a volume of siafunds that is created and spent as an
// atomic unit.
type SiafundElement struct {
	StateElement
	SiafundOutput
	ClaimStart Currency // value of SiafundPool when element was created
}

// A FileContractElement is a storage agreement between a renter and a host.
type FileContractElement struct {
	StateElement
	FileContract
}

// An Attestation associates a key-value pair with an identity. For example,
// hosts attest to their network address by setting Key to "HostAnnouncement"
// and Value to their address, thereby allowing renters to discover them.
// Generally, an attestation for a particular key is considered to overwrite any
// previous attestations with the same key. (This allows hosts to announce a new
// network address, for example.)
type Attestation struct {
	PublicKey PublicKey
	Key       string
	Value     []byte
	Signature Signature
}

// A Transaction transfers value by consuming existing Outputs and creating new
// Outputs.
type Transaction struct {
	SiacoinInputs           []SiacoinInput
	SiacoinOutputs          []SiacoinOutput
	SiafundInputs           []SiafundInput
	SiafundOutputs          []SiafundOutput
	FileContracts           []FileContract
	FileContractRevisions   []FileContractRevision
	FileContractResolutions []FileContractResolution
	Attestations            []Attestation
	ArbitraryData           []byte
	NewFoundationAddress    Address
	MinerFee                Currency
}

// ID returns the "semantic hash" of the transaction, covering all of the
// transaction's effects, but not incidental data such as signatures or Merkle
// proofs. This ensures that the ID will remain stable (i.e. non-malleable).
//
// To hash all of the data in a transaction, use the EncodeTo method.
func (txn *Transaction) ID() TransactionID {
	// NOTE: In general, it is not possible to change a transaction's ID without
	// causing it to become invalid, but an exception exists for non-standard
	// spend policies. Consider a policy that may be satisfied by either a
	// signature or a timelock. If a transaction is broadcast that signs the
	// input, and the timelock has expired, then anyone may remove the signature
	// from the input without invalidating the transaction. Of course, the net
	// result will be the same, so arguably there's little reason to care. You
	// only need to worry about this if you're hashing the full transaction data
	// for some reason.
	h := hasherPool.Get().(*Hasher)
	defer hasherPool.Put(h)
	h.Reset()
	h.E.WriteString("sia/id/transaction")
	h.E.WritePrefix(len(txn.SiacoinInputs))
	for _, in := range txn.SiacoinInputs {
		in.Parent.ID.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.SiacoinOutputs))
	for _, out := range txn.SiacoinOutputs {
		out.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.SiafundInputs))
	for _, in := range txn.SiafundInputs {
		in.Parent.ID.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.SiafundOutputs))
	for _, out := range txn.SiafundOutputs {
		out.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.FileContracts))
	for _, fc := range txn.FileContracts {
		fc.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.FileContractRevisions))
	for _, fcr := range txn.FileContractRevisions {
		fcr.Parent.ID.EncodeTo(h.E)
		fcr.Revision.EncodeTo(h.E)
	}
	h.E.WritePrefix(len(txn.FileContractResolutions))
	for _, fcr := range txn.FileContractResolutions {
		fcr.Parent.ID.EncodeTo(h.E)
		fcr.Renewal.EncodeTo(h.E)
		fcr.StorageProof.WindowStart.EncodeTo(h.E)
		fcr.Finalization.EncodeTo(h.E)
	}
	for _, a := range txn.Attestations {
		a.EncodeTo(h.E)
	}
	h.E.WriteBytes(txn.ArbitraryData)
	txn.NewFoundationAddress.EncodeTo(h.E)
	txn.MinerFee.EncodeTo(h.E)
	return TransactionID(h.Sum())
}

// DeepCopy returns a copy of txn that does not alias any of its memory.
func (txn *Transaction) DeepCopy() Transaction {
	c := *txn
	c.SiacoinInputs = append([]SiacoinInput(nil), c.SiacoinInputs...)
	for i := range c.SiacoinInputs {
		c.SiacoinInputs[i].Parent.MerkleProof = append([]Hash256(nil), c.SiacoinInputs[i].Parent.MerkleProof...)
		c.SiacoinInputs[i].Signatures = append([]Signature(nil), c.SiacoinInputs[i].Signatures...)
	}
	c.SiacoinOutputs = append([]SiacoinOutput(nil), c.SiacoinOutputs...)
	c.SiafundInputs = append([]SiafundInput(nil), c.SiafundInputs...)
	for i := range c.SiafundInputs {
		c.SiafundInputs[i].Parent.MerkleProof = append([]Hash256(nil), c.SiafundInputs[i].Parent.MerkleProof...)
		c.SiafundInputs[i].Signatures = append([]Signature(nil), c.SiafundInputs[i].Signatures...)
	}
	c.SiafundOutputs = append([]SiafundOutput(nil), c.SiafundOutputs...)
	c.FileContracts = append([]FileContract(nil), c.FileContracts...)
	c.FileContractRevisions = append([]FileContractRevision(nil), c.FileContractRevisions...)
	for i := range c.FileContractRevisions {
		c.FileContractRevisions[i].Parent.MerkleProof = append([]Hash256(nil), c.FileContractRevisions[i].Parent.MerkleProof...)
	}
	c.FileContractResolutions = append([]FileContractResolution(nil), c.FileContractResolutions...)
	for i := range c.FileContractResolutions {
		c.FileContractResolutions[i].Parent.MerkleProof = append([]Hash256(nil), c.FileContractResolutions[i].Parent.MerkleProof...)
		c.FileContractResolutions[i].StorageProof.WindowProof = append([]Hash256(nil), c.FileContractResolutions[i].StorageProof.WindowProof...)
		c.FileContractResolutions[i].StorageProof.Proof = append([]Hash256(nil), c.FileContractResolutions[i].StorageProof.Proof...)
	}
	for i := range c.Attestations {
		c.Attestations[i].Value = append([]byte(nil), c.Attestations[i].Value...)
	}
	c.ArbitraryData = append([]byte(nil), c.ArbitraryData...)
	return c
}

// SiacoinOutputID returns the ID of the siacoin output at index i.
func (txn *Transaction) SiacoinOutputID(i int) ElementID {
	return ElementID{
		Source: Hash256(txn.ID()),
		Index:  uint64(i),
	}
}

// SiafundClaimOutputID returns the ID of the siacoin claim output for the
// siafund input at index i.
func (txn *Transaction) SiafundClaimOutputID(i int) ElementID {
	return ElementID{
		Source: Hash256(txn.ID()),
		Index:  uint64(len(txn.SiacoinOutputs) + i),
	}
}

// SiafundOutputID returns the ID of the siafund output at index i.
func (txn *Transaction) SiafundOutputID(i int) ElementID {
	return ElementID{
		Source: Hash256(txn.ID()),
		Index:  uint64(len(txn.SiacoinOutputs) + len(txn.SiafundInputs) + i),
	}
}

// FileContractID returns the ID of the file contract at index i.
func (txn *Transaction) FileContractID(i int) ElementID {
	return ElementID{
		Source: Hash256(txn.ID()),
		Index:  uint64(len(txn.SiacoinOutputs) + len(txn.SiafundInputs) + len(txn.SiafundOutputs) + i),
	}
}

// EphemeralSiacoinElement returns txn.SiacoinOutputs[i] as an ephemeral
// SiacoinElement.
func (txn *Transaction) EphemeralSiacoinElement(i int) SiacoinElement {
	return SiacoinElement{
		StateElement: StateElement{
			ID:        txn.SiacoinOutputID(0),
			LeafIndex: EphemeralLeafIndex,
		},
		SiacoinOutput: txn.SiacoinOutputs[0],
	}
}

// A BlockHeader contains a Block's non-transaction data.
type BlockHeader struct {
	Height       uint64
	ParentID     BlockID
	Nonce        uint64
	Timestamp    time.Time
	MinerAddress Address
	Commitment   Hash256
}

// Index returns the header's chain index.
func (h BlockHeader) Index() ChainIndex {
	return ChainIndex{
		Height: h.Height,
		ID:     h.ID(),
	}
}

// ParentIndex returns the index of the header's parent.
func (h BlockHeader) ParentIndex() ChainIndex {
	return ChainIndex{
		Height: h.Height - 1,
		ID:     h.ParentID,
	}
}

// ID returns a hash that uniquely identifies a block.
func (h BlockHeader) ID() BlockID {
	// NOTE: although in principle we only need to hash 48 bytes of data, we
	// must ensure compatibility with existing Sia mining hardware, which
	// expects an 80-byte buffer with the nonce at [32:40].
	buf := make([]byte, 32+8+8+32)
	copy(buf[0:], "sia/id/block")
	binary.LittleEndian.PutUint64(buf[32:], h.Nonce)
	binary.LittleEndian.PutUint64(buf[40:], uint64(h.Timestamp.Unix()))
	copy(buf[48:], h.Commitment[:])
	return BlockID(HashBytes(buf))
}

// CurrentTimestamp returns the current time, rounded to the nearest second. The
// time zone is set to UTC.
func CurrentTimestamp() time.Time { return time.Now().Round(time.Second).UTC() }

// A Block is a set of transactions grouped under a header.
type Block struct {
	Header       BlockHeader
	Transactions []Transaction
}

// ID returns a hash that uniquely identifies a block. It is equivalent to
// b.Header.ID().
func (b *Block) ID() BlockID { return b.Header.ID() }

// Index returns the block's chain index. It is equivalent to b.Header.Index().
func (b *Block) Index() ChainIndex { return b.Header.Index() }

// MinerOutputID returns the output ID of the miner payout.
func (b *Block) MinerOutputID() ElementID {
	return ElementID{
		Source: Hash256(b.ID()),
		Index:  0,
	}
}

// FoundationOutputID returns the output ID of the foundation payout. A
// Foundation subsidy output is only created every 4380 blocks after the
// hardfork at block 298000.
func (b *Block) FoundationOutputID() ElementID {
	return ElementID{
		Source: Hash256(b.ID()),
		Index:  1,
	}
}

// Work represents a quantity of work.
type Work struct {
	// The representation is the expected number of hashes required to produce a
	// given hash, in big-endian order.
	NumHashes [32]byte
}

// Add returns w+v, wrapping on overflow.
func (w Work) Add(v Work) Work {
	var r Work
	var sum, c uint64
	for i := 24; i >= 0; i -= 8 {
		wi := binary.BigEndian.Uint64(w.NumHashes[i:])
		vi := binary.BigEndian.Uint64(v.NumHashes[i:])
		sum, c = bits.Add64(wi, vi, c)
		binary.BigEndian.PutUint64(r.NumHashes[i:], sum)
	}
	return r
}

// Sub returns w-v, wrapping on underflow.
func (w Work) Sub(v Work) Work {
	var r Work
	var sum, c uint64
	for i := 24; i >= 0; i -= 8 {
		wi := binary.BigEndian.Uint64(w.NumHashes[i:])
		vi := binary.BigEndian.Uint64(v.NumHashes[i:])
		sum, c = bits.Sub64(wi, vi, c)
		binary.BigEndian.PutUint64(r.NumHashes[i:], sum)
	}
	return r
}

// Mul64 returns w*v, wrapping on overflow.
func (w Work) Mul64(v uint64) Work {
	var r Work
	var c uint64
	for i := 24; i >= 0; i -= 8 {
		wi := binary.BigEndian.Uint64(w.NumHashes[i:])
		hi, prod := bits.Mul64(wi, v)
		prod, cc := bits.Add64(prod, c, 0)
		c = hi + cc
		binary.BigEndian.PutUint64(r.NumHashes[i:], prod)
	}
	return r
}

// Div64 returns w/v.
func (w Work) Div64(v uint64) Work {
	var r Work
	var quo, rem uint64
	for i := 0; i < len(w.NumHashes); i += 8 {
		wi := binary.BigEndian.Uint64(w.NumHashes[i:])
		quo, rem = bits.Div64(rem, wi, v)
		binary.BigEndian.PutUint64(r.NumHashes[i:], quo)
	}
	return r
}

// Cmp compares two work values.
func (w Work) Cmp(v Work) int {
	return bytes.Compare(w.NumHashes[:], v.NumHashes[:])
}

// WorkRequiredForHash estimates how much work was required to produce the given
// id. Note that the mapping is not injective; many different ids may require
// the same expected amount of Work.
func WorkRequiredForHash(id BlockID) Work {
	if id == (BlockID{}) {
		// This should never happen as long as inputs are properly validated and
		// the laws of physics are intact.
		panic("impossibly good BlockID")
	}
	// As a special case, this hash requires the maximum possible amount of
	// Work. (Otherwise, the division would produce 2^256, which overflows our
	// representation.)
	if id == ([32]byte{31: 1}) {
		return Work{
			NumHashes: [32]byte{
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
			},
		}
	}

	// To get the expected number of hashes required, simply divide 2^256 by id.
	//
	// TODO: write a zero-alloc uint256 division instead of using big.Int
	maxTarget := new(big.Int).Lsh(big.NewInt(1), 256)
	idInt := new(big.Int).SetBytes(id[:])
	quo := maxTarget.Div(maxTarget, idInt)
	var w Work
	quo.FillBytes(w.NumHashes[:])
	return w
}

// HashRequiringWork returns the best BlockID that the given amount of Work
// would be expected to produce. Note that many different BlockIDs may require
// the same amount of Work; this function returns the lowest of them.
func HashRequiringWork(w Work) BlockID {
	if w.NumHashes == ([32]byte{}) {
		panic("no hash requires zero work")
	}
	// As a special case, 1 Work produces this hash. (Otherwise, the division
	// would produce 2^256, which overflows our representation.)
	if w.NumHashes == ([32]byte{31: 1}) {
		return BlockID{
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		}
	}
	maxTarget := new(big.Int).Lsh(big.NewInt(1), 256)
	workInt := new(big.Int).SetBytes(w.NumHashes[:])
	quo := maxTarget.Div(maxTarget, workInt)
	var id BlockID
	quo.FillBytes(id[:])
	return id
}

// HashBytes computes the hash of b using Sia's hash function.
func HashBytes(b []byte) Hash256 { return blake2b.Sum256(b) }

// Pool for reducing heap allocations when hashing. This is only necessary
// because blake2b.New256 returns a hash.Hash interface, which prevents the
// compiler from doing escape analysis. Can be removed if we switch to an
// implementation whose constructor returns a concrete type.
var hasherPool = &sync.Pool{New: func() interface{} { return NewHasher() }}

// Implementations of fmt.Stringer, encoding.Text(Un)marshaler, and json.(Un)marshaler

func stringerHex(prefix string, data []byte) string {
	return prefix + ":" + hex.EncodeToString(data[:])
}

func marshalHex(prefix string, data []byte) ([]byte, error) {
	return []byte(stringerHex(prefix, data)), nil
}

func unmarshalHex(dst []byte, prefix string, data []byte) error {
	n, err := hex.Decode(dst, bytes.TrimPrefix(data, []byte(prefix+":")))
	if n < len(dst) {
		err = io.EOF
	}
	if err != nil {
		return fmt.Errorf("decoding %v:<hex> failed: %w", prefix, err)
	}
	return nil
}

func marshalJSONHex(prefix string, data []byte) ([]byte, error) {
	return []byte(`"` + stringerHex(prefix, data) + `"`), nil
}

func unmarshalJSONHex(dst []byte, prefix string, data []byte) error {
	return unmarshalHex(dst, prefix, bytes.Trim(data, `"`))
}

// String implements fmt.Stringer.
func (h Hash256) String() string { return stringerHex("h", h[:]) }

// MarshalText implements encoding.TextMarshaler.
func (h Hash256) MarshalText() ([]byte, error) { return marshalHex("h", h[:]) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *Hash256) UnmarshalText(b []byte) error { return unmarshalHex(h[:], "h", b) }

// MarshalJSON implements json.Marshaler.
func (h Hash256) MarshalJSON() ([]byte, error) { return marshalJSONHex("h", h[:]) }

// UnmarshalJSON implements json.Unmarshaler.
func (h *Hash256) UnmarshalJSON(b []byte) error { return unmarshalJSONHex(h[:], "h", b) }

// String implements fmt.Stringer.
func (ci ChainIndex) String() string {
	// use the 4 least-significant bytes of ID -- in a mature chain, the
	// most-significant bytes will be zeros
	return fmt.Sprintf("%d::%x", ci.Height, ci.ID[len(ci.ID)-4:])
}

// MarshalText implements encoding.TextMarshaler.
func (ci ChainIndex) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%d::%x", ci.Height, ci.ID[:])), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (ci *ChainIndex) UnmarshalText(b []byte) (err error) {
	parts := bytes.Split(b, []byte("::"))
	if len(parts) != 2 {
		return fmt.Errorf("decoding <height>::<id> failed: wrong number of separators")
	} else if ci.Height, err = strconv.ParseUint(string(parts[0]), 10, 64); err != nil {
		return fmt.Errorf("decoding <height>::<id> failed: %w", err)
	} else if n, err := hex.Decode(ci.ID[:], parts[1]); err != nil {
		return fmt.Errorf("decoding <height>::<id> failed: %w", err)
	} else if n < len(ci.ID) {
		return fmt.Errorf("decoding <height>::<id> failed: %w", io.EOF)
	}
	return nil
}

// ParseChainIndex parses a chain index from a string.
func ParseChainIndex(s string) (ci ChainIndex, err error) {
	err = ci.UnmarshalText([]byte(s))
	return
}

// String implements fmt.Stringer.
func (eid ElementID) String() string {
	return fmt.Sprintf("elem:%x:%v", eid.Source[:], eid.Index)
}

// MarshalText implements encoding.TextMarshaler.
func (eid ElementID) MarshalText() ([]byte, error) { return []byte(eid.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (eid *ElementID) UnmarshalText(b []byte) (err error) {
	parts := bytes.Split(b, []byte(":"))
	if len(parts) != 3 {
		return fmt.Errorf("decoding <hex>:<index> failed: wrong number of separators")
	} else if n, err := hex.Decode(eid.Source[:], parts[1]); err != nil {
		return fmt.Errorf("decoding <hex>:<index> failed: %w", err)
	} else if n < len(eid.Source) {
		return fmt.Errorf("decoding <hex>:<index> failed: %w", io.EOF)
	} else if eid.Index, err = strconv.ParseUint(string(parts[2]), 10, 64); err != nil {
		return fmt.Errorf("decoding <hex>:<index> failed: %w", err)
	}
	return nil
}

// String implements fmt.Stringer.
func (a Address) String() string {
	checksum := HashBytes(a[:])
	return stringerHex("addr", append(a[:], checksum[:6]...))
}

// MarshalText implements encoding.TextMarshaler.
func (a Address) MarshalText() ([]byte, error) { return []byte(a.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (a *Address) UnmarshalText(b []byte) (err error) {
	withChecksum := make([]byte, 32+6)
	n, err := hex.Decode(withChecksum, bytes.TrimPrefix(b, []byte("addr:")))
	if err != nil {
		err = fmt.Errorf("decoding addr:<hex> failed: %w", err)
	} else if n != len(withChecksum) {
		err = fmt.Errorf("decoding addr:<hex> failed: %w", io.EOF)
	} else if checksum := HashBytes(withChecksum[:32]); !bytes.Equal(checksum[:6], withChecksum[32:]) {
		err = errors.New("bad checksum")
	}
	copy(a[:], withChecksum[:32])
	return
}

// MarshalJSON implements json.Marshaler.
func (a Address) MarshalJSON() ([]byte, error) {
	checksum := HashBytes(a[:])
	return marshalJSONHex("addr", append(a[:], checksum[:6]...))
}

// UnmarshalJSON implements json.Unmarshaler.
func (a *Address) UnmarshalJSON(b []byte) (err error) {
	return a.UnmarshalText(bytes.Trim(b, `"`))
}

// ParseAddress parses an address from a prefixed hex encoded string.
func ParseAddress(s string) (a Address, err error) {
	err = a.UnmarshalText([]byte(s))
	return
}

// String implements fmt.Stringer.
func (bid BlockID) String() string { return stringerHex("bid", bid[:]) }

// MarshalText implements encoding.TextMarshaler.
func (bid BlockID) MarshalText() ([]byte, error) { return marshalHex("bid", bid[:]) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (bid *BlockID) UnmarshalText(b []byte) error { return unmarshalHex(bid[:], "bid", b) }

// MarshalJSON implements json.Marshaler.
func (bid BlockID) MarshalJSON() ([]byte, error) { return marshalJSONHex("bid", bid[:]) }

// UnmarshalJSON implements json.Unmarshaler.
func (bid *BlockID) UnmarshalJSON(b []byte) error { return unmarshalJSONHex(bid[:], "bid", b) }

// String implements fmt.Stringer.
func (pk PublicKey) String() string { return stringerHex("ed25519", pk[:]) }

// MarshalText implements encoding.TextMarshaler.
func (pk PublicKey) MarshalText() ([]byte, error) { return marshalHex("ed25519", pk[:]) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (pk *PublicKey) UnmarshalText(b []byte) error { return unmarshalHex(pk[:], "ed25519", b) }

// MarshalJSON implements json.Marshaler.
func (pk PublicKey) MarshalJSON() ([]byte, error) { return marshalJSONHex("ed25519", pk[:]) }

// UnmarshalJSON implements json.Unmarshaler.
func (pk *PublicKey) UnmarshalJSON(b []byte) error { return unmarshalJSONHex(pk[:], "ed25519", b) }

// String implements fmt.Stringer.
func (tid TransactionID) String() string { return stringerHex("txid", tid[:]) }

// MarshalText implements encoding.TextMarshaler.
func (tid TransactionID) MarshalText() ([]byte, error) { return marshalHex("txid", tid[:]) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (tid *TransactionID) UnmarshalText(b []byte) error { return unmarshalHex(tid[:], "txid", b) }

// MarshalJSON implements json.Marshaler.
func (tid TransactionID) MarshalJSON() ([]byte, error) { return marshalJSONHex("txid", tid[:]) }

// UnmarshalJSON implements json.Unmarshaler.
func (tid *TransactionID) UnmarshalJSON(b []byte) error { return unmarshalJSONHex(tid[:], "txid", b) }

// String implements fmt.Stringer.
func (sig Signature) String() string { return stringerHex("sig", sig[:]) }

// MarshalText implements encoding.TextMarshaler.
func (sig Signature) MarshalText() ([]byte, error) { return marshalHex("sig", sig[:]) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (sig *Signature) UnmarshalText(b []byte) error { return unmarshalHex(sig[:], "sig", b) }

// MarshalJSON implements json.Marshaler.
func (sig Signature) MarshalJSON() ([]byte, error) { return marshalJSONHex("sig", sig[:]) }

// UnmarshalJSON implements json.Unmarshaler.
func (sig *Signature) UnmarshalJSON(b []byte) error { return unmarshalJSONHex(sig[:], "sig", b) }

// String implements fmt.Stringer.
func (w Work) String() string { return new(big.Int).SetBytes(w.NumHashes[:]).String() }

// MarshalText implements encoding.TextMarshaler.
func (w Work) MarshalText() ([]byte, error) {
	return new(big.Int).SetBytes(w.NumHashes[:]).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (w *Work) UnmarshalText(b []byte) error {
	i := new(big.Int)
	if err := i.UnmarshalText(b); err != nil {
		return err
	} else if i.Sign() < 0 {
		return errors.New("value cannot be negative")
	} else if i.BitLen() > 256 {
		return errors.New("value overflows Work representation")
	}
	i.FillBytes(w.NumHashes[:])
	return nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (w *Work) UnmarshalJSON(b []byte) error {
	return w.UnmarshalText(bytes.Trim(b, `"`))
}

// MarshalJSON implements json.Marshaler.
func (w Work) MarshalJSON() ([]byte, error) {
	js, err := new(big.Int).SetBytes(w.NumHashes[:]).MarshalJSON()
	return []byte(`"` + string(js) + `"`), err
}
