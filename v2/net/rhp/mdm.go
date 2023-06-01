package rhp

import (
	"go.sia.tech/core/v2/net/rpc"
	"go.sia.tech/core/v2/types"
)

const (
	blocksPerYear = 144 * 365
)

// Specifiers for MDM instructions
var (
	SpecInstrAppendSector     = rpc.NewSpecifier("AppendSector")
	SpecInstrUpdateSector     = rpc.NewSpecifier("UpdateSector")
	SpecInstrDropSectors      = rpc.NewSpecifier("DropSectors")
	SpecInstrHasSector        = rpc.NewSpecifier("HasSector")
	SpecInstrReadOffset       = rpc.NewSpecifier("ReadOffset")
	SpecInstrReadSector       = rpc.NewSpecifier("ReadSector")
	SpecInstrContractRevision = rpc.NewSpecifier("Revision")
	SpecInstrSectorRoots      = rpc.NewSpecifier("SectorRoots")
	SpecInstrSwapSector       = rpc.NewSpecifier("SwapSector")
	SpecInstrUpdateRegistry   = rpc.NewSpecifier("UpdateRegistry")
	SpecInstrReadRegistry     = rpc.NewSpecifier("ReadRegistry")
	SpecInstrReadRegistrySID  = rpc.NewSpecifier("ReadRegistrySID")
)

// An Instruction is a single instruction in an MDM program.
type Instruction interface {
	isInstruction()
	rpc.Object
}

func (InstrAppendSector) isInstruction()     {}
func (InstrUpdateSector) isInstruction()     {}
func (InstrContractRevision) isInstruction() {}
func (InstrSectorRoots) isInstruction()      {}
func (InstrDropSectors) isInstruction()      {}
func (InstrHasSector) isInstruction()        {}
func (InstrReadOffset) isInstruction()       {}
func (InstrReadRegistry) isInstruction()     {}
func (InstrReadSector) isInstruction()       {}
func (InstrSwapSector) isInstruction()       {}
func (InstrUpdateRegistry) isInstruction()   {}

// InstructionRequiresContract returns true if the instruction requires a
// contract to be locked.
func InstructionRequiresContract(i Instruction) bool {
	switch i.(type) {
	case *InstrAppendSector,
		*InstrUpdateSector,
		*InstrContractRevision,
		*InstrSectorRoots,
		*InstrDropSectors,
		*InstrSwapSector:
		return true
	case *InstrHasSector,
		*InstrReadOffset,
		*InstrReadRegistry,
		*InstrReadSector,
		*InstrUpdateRegistry:
		return false
	}
	panic("unahndled instruction")
}

// InstructionRequiresFinalization returns true if the instruction results need
// to be committed to a contract.
func InstructionRequiresFinalization(i Instruction) bool {
	switch i.(type) {
	case *InstrAppendSector,
		*InstrUpdateSector,
		*InstrDropSectors,
		*InstrSwapSector:
		return true
	case *InstrContractRevision,
		*InstrSectorRoots,
		*InstrHasSector,
		*InstrReadOffset,
		*InstrReadRegistry,
		*InstrReadSector,
		*InstrUpdateRegistry:
		return false
	}
	panic("unahndled instruction")
}

// InstrAppendSector uploads and appends a new sector to a contract
type InstrAppendSector struct {
	SectorDataOffset uint64
	ProofRequired    bool
}

// MaxLen implements rpc.Object
func (i *InstrAppendSector) MaxLen() int {
	return 9
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrAppendSector) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.SectorDataOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrAppendSector) DecodeFrom(d *types.Decoder) {
	i.SectorDataOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrUpdateSector uploads and appends a new sector to a contract
type InstrUpdateSector struct {
	Offset        uint64
	Length        uint64
	DataOffset    uint64
	ProofRequired bool
}

// MaxLen implements rpc.Object
func (i *InstrUpdateSector) MaxLen() int {
	return 25
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrUpdateSector) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.Offset)
	e.WriteUint64(i.Length)
	e.WriteUint64(i.DataOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrUpdateSector) DecodeFrom(d *types.Decoder) {
	i.Offset = d.ReadUint64()
	i.Length = d.ReadUint64()
	i.DataOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrContractRevision returns the latest revision of the program's contract.
type InstrContractRevision struct {
}

// MaxLen implements rpc.Object
func (i *InstrContractRevision) MaxLen() int {
	return 0
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrContractRevision) EncodeTo(e *types.Encoder) {
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrContractRevision) DecodeFrom(d *types.Decoder) {
}

// InstrSectorRoots returns the program's sector roots
type InstrSectorRoots struct {
}

// MaxLen implements rpc.Object
func (i *InstrSectorRoots) MaxLen() int {
	return 0
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrSectorRoots) EncodeTo(e *types.Encoder) {
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrSectorRoots) DecodeFrom(d *types.Decoder) {
}

// InstrDropSectors deletes a number of sectors from the end of the contract.
type InstrDropSectors struct {
	SectorCountOffset uint64
	ProofRequired     bool
}

// MaxLen implements rpc.Object
func (i *InstrDropSectors) MaxLen() int {
	return 9
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrDropSectors) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.SectorCountOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrDropSectors) DecodeFrom(d *types.Decoder) {
	i.SectorCountOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrHasSector returns true if the host has the given sector.
type InstrHasSector struct {
	SectorRootOffset uint64
}

// MaxLen implements rpc.Object
func (i *InstrHasSector) MaxLen() int {
	return 8
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrHasSector) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.SectorRootOffset)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrHasSector) DecodeFrom(d *types.Decoder) {
	i.SectorRootOffset = d.ReadUint64()
}

// InstrReadOffset reads len bytes from the contract at the given offset.
type InstrReadOffset struct {
	DataOffset    uint64
	LengthOffset  uint64
	ProofRequired bool
}

// MaxLen implements rpc.Object
func (i *InstrReadOffset) MaxLen() int {
	return 17
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrReadOffset) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.DataOffset)
	e.WriteUint64(i.LengthOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrReadOffset) DecodeFrom(d *types.Decoder) {
	i.DataOffset = d.ReadUint64()
	i.LengthOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrReadRegistry reads the given registry key from the contract.
type InstrReadRegistry struct {
	PublicKeyOffset uint64
	TweakOffset     uint64
}

// MaxLen implements rpc.Object
func (i *InstrReadRegistry) MaxLen() int {
	return 16
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrReadRegistry) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.PublicKeyOffset)
	e.WriteUint64(i.TweakOffset)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrReadRegistry) DecodeFrom(d *types.Decoder) {
	i.PublicKeyOffset = d.ReadUint64()
	i.TweakOffset = d.ReadUint64()
}

// InstrReadSector reads offset and len bytes of the sector.
type InstrReadSector struct {
	RootOffset    uint64
	SectorOffset  uint64
	LengthOffset  uint64
	ProofRequired bool
}

// MaxLen implements rpc.Object
func (i *InstrReadSector) MaxLen() int {
	return 25
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrReadSector) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.RootOffset)
	e.WriteUint64(i.SectorOffset)
	e.WriteUint64(i.LengthOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrReadSector) DecodeFrom(d *types.Decoder) {
	i.RootOffset = d.ReadUint64()
	i.SectorOffset = d.ReadUint64()
	i.LengthOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrSwapSector swaps two sectors by root in the contract.
type InstrSwapSector struct {
	RootAOffset   uint64
	RootBOffset   uint64
	ProofRequired bool
}

// MaxLen implements rpc.Object
func (i *InstrSwapSector) MaxLen() int {
	return 17
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrSwapSector) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.RootAOffset)
	e.WriteUint64(i.RootBOffset)
	e.WriteBool(i.ProofRequired)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrSwapSector) DecodeFrom(d *types.Decoder) {
	i.RootAOffset = d.ReadUint64()
	i.RootBOffset = d.ReadUint64()
	i.ProofRequired = d.ReadBool()
}

// InstrUpdateRegistry updates a registry entry.
type InstrUpdateRegistry struct {
	EntryOffset uint64
}

// MaxLen implements rpc.Object
func (i *InstrUpdateRegistry) MaxLen() int {
	return 8
}

// EncodeTo encodes an instruction to the provided encoder. Implements
// rpc.Object.
func (i *InstrUpdateRegistry) EncodeTo(e *types.Encoder) {
	e.WriteUint64(i.EntryOffset)
}

// DecodeFrom decodes an instruction from the provided decoder. Implements
// rpc.Object.
func (i *InstrUpdateRegistry) DecodeFrom(d *types.Decoder) {
	i.EntryOffset = d.ReadUint64()
}

// ResourceUsage is the associated costs of executing an instruction set or
// individual instruction.
type ResourceUsage struct {
	// BaseCost is the cost to execute the instruction and includes
	// resource costs like memory and time.
	BaseCost types.Currency
	// StorageCost cost is charged after successful completion
	// of the instruction and should be refunded if the program fails.
	StorageCost types.Currency
	// AdditionalCollateral cost is the additional collateral the host should
	// add during program finalization
	AdditionalCollateral types.Currency

	Memory uint64
	Time   uint64
}

// Add returns the sum of r and b.
func (r ResourceUsage) Add(b ResourceUsage) (c ResourceUsage) {
	c.BaseCost = r.BaseCost.Add(b.BaseCost)
	c.StorageCost = r.StorageCost.Add(b.StorageCost)
	c.AdditionalCollateral = r.AdditionalCollateral.Add(b.AdditionalCollateral)

	c.Memory += b.Memory
	c.Time += b.Time
	return c
}

// resourceCost returns the cost of a program with the given data and time
func resourceCost(settings HostSettings, memory, time uint64) types.Currency {
	return settings.ProgMemoryTimeCost.Mul64(memory * time)
}

// writeCost returns the cost of writing the instructions data to disk.
func writeCost(settings HostSettings, n uint64) types.Currency {
	// Atomic write size for modern disks is 4kib so we round up.
	atomicWriteSize := uint64(1 << 12)
	if mod := n % atomicWriteSize; mod != 0 {
		n += (atomicWriteSize - mod)
	}
	return settings.ProgWriteCost.Mul64(n)
}

// initCost returns the cost of initializing a program.
func initCost(settings HostSettings, data, instructions uint64) (costs ResourceUsage) {
	time := 1 + instructions
	costs.BaseCost = settings.ProgInitBaseCost.Add(resourceCost(settings, data, time))
	costs.Memory = 1 << 20
	return
}

// finalizationCost returns the cost of finalizing a program.
//
// note: siad's finalize cost uses the program's total memory usage, but memory
// cost is already included in the instruction's base cost.
func finalizationCost(settings HostSettings) (costs ResourceUsage) {
	costs.Memory = 1000
	costs.Time = 50000
	costs.BaseCost = resourceCost(settings, costs.Memory, costs.Time)
	return
}

// ExecutionCost returns the cost of initializing and, optionally, finalizing a
// program.
func ExecutionCost(settings HostSettings, data, instructions uint64, requiresFinalization bool) (costs ResourceUsage) {
	costs = initCost(settings, data, instructions)
	if requiresFinalization {
		costs = costs.Add(finalizationCost(settings))
	}
	return
}

// AppendSectorCost returns the cost of the append sector instruction.
func AppendSectorCost(settings HostSettings, duration uint64) (costs ResourceUsage) {
	costs.Memory = SectorSize
	costs.Time = 10000

	// base cost is cost of writing 1 sector and storing 1 sector in memory.
	// note: in siad the memory cost is calculated using the program's total
	// memory, here I've opted to use only the instruction's memory.
	costs.BaseCost = settings.InstrAppendSectorBaseCost.Add(writeCost(settings, SectorSize)).Add(resourceCost(settings, costs.Memory, costs.Time))
	// storage cost is the cost of storing 1 sector for the remaining duration.
	costs.StorageCost = settings.StoragePrice.Mul64(SectorSize * duration)
	// additional collateral is the collateral the host is expected to put up
	// per sector per block.
	// note: in siad the additional collateral does not consider remaining
	// duration.
	costs.AdditionalCollateral = settings.Collateral.Mul64(SectorSize * duration)
	return
}

// UpdateSectorCost returns the cost of the update instruction.
func UpdateSectorCost(settings HostSettings, l uint64) (costs ResourceUsage) {
	costs.Memory = l + SectorSize
	costs.Time = 10000

	// base cost is cost of reading and writing 1 sector
	costs = ReadCost(settings, SectorSize)
	costs.BaseCost = costs.BaseCost.Add(settings.InstrUpdateSectorBaseCost).Add(writeCost(settings, SectorSize)).Add(resourceCost(settings, costs.Memory, costs.Time))
	return
}

// DropSectorsCost returns the cost of the drop sectors instruction.
func DropSectorsCost(settings HostSettings, n uint64) (costs ResourceUsage) {
	costs.BaseCost = settings.InstrDropSectorsUnitCost.Mul64(n).Add(settings.InstrDropSectorsBaseCost)
	return
}

// HasSectorCost returns the cost of the has sector instruction.
func HasSectorCost(settings HostSettings) (costs ResourceUsage) {
	costs.BaseCost = settings.InstrHasSectorBaseCost
	return
}

// ReadCost returns the cost of the read instruction.
func ReadCost(settings HostSettings, l uint64) (costs ResourceUsage) {
	costs.BaseCost = settings.ProgReadCost.Mul64(l).Add(settings.InstrReadBaseCost)
	return
}

// RevisionCost returns the cost of the revision instruction.
func RevisionCost(settings HostSettings) (costs ResourceUsage) {
	costs.BaseCost = settings.InstrRevisionBaseCost
	return
}

// SectorRootsCost returns the cost of executing the contract roots instruction.
func SectorRootsCost(settings HostSettings, sectors uint64) (costs ResourceUsage) {
	costs.Memory = 32 * sectors
	costs.Time = 10000
	costs.BaseCost = settings.InstrSectorRootsBaseCost.Add(resourceCost(settings, costs.Memory, costs.Time))
	return
}

// SwapSectorCost returns the cost of the swap sector instruction.
func SwapSectorCost(settings HostSettings) (costs ResourceUsage) {
	costs.BaseCost = settings.InstrSwapSectorBaseCost
	return
}

// UpdateRegistryCost returns the cost of the update registry instruction.
func UpdateRegistryCost(settings HostSettings) (costs ResourceUsage) {
	costs.BaseCost = writeCost(settings, 256).Add(settings.InstrUpdateRegistryBaseCost)
	// storing 256 bytes for 5 years
	costs.StorageCost = settings.StoragePrice.Mul64(256 * 5 * blocksPerYear)
	return
}

// ReadRegistryCost returns the cost of the read registry instruction.
func ReadRegistryCost(settings HostSettings) (costs ResourceUsage) {
	costs.BaseCost = writeCost(settings, 256).Add(settings.InstrReadRegistryBaseCost)
	// storing 256 bytes for 10 years
	costs.StorageCost = settings.StoragePrice.Mul64(256 * 10 * blocksPerYear)
	return
}
