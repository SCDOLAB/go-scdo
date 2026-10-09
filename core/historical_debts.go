package core

// historicalAbsentDebtRow is one canonical debt whose source transaction is
// not on the canonical source shard. Scanned from the public Classic
// JSON-RPC nodes (fork height 2979594 through the tip on 2026-10-09).
// Do not add a pair by hand.
type historicalAbsentDebtRow struct {
	shard  uint32
	height uint64
	hash   string
}

var historicalAbsentDebtRows = []historicalAbsentDebtRow{
	{1, 5162247, "0x46c6d99968db700d744b6542b05e32453e48c11d8a52a1698d4e631696e3b259"},
	{1, 5162248, "0xe4cc64a5de44a054af3576b725d0b81f5086dda64d8b47d91f062c268c111ec4"},
	{1, 5162259, "0x3c02364b9c0afea96ebb907b33976846067271343165f169892bbda9708739cf"},
	{1, 5162259, "0x73540d199fb397448e569d91258141f43ebf2ddea50a1cabb7bcde8e35937292"},
	{1, 5162260, "0x5de0add58ba66b36310d70f2ce7980390cf98544d4e254e30795ce15f3ce7378"},
	{1, 5162260, "0xdc05c1962366b48820e3d053cf73ef0e029fbaca512368283bd4179880dc4094"},
	{1, 6124420, "0x17578564a079bc1b0a83d9128a8ac43f343c2f2e04d7a0d10598a66930c7ca4a"},
	{1, 6126398, "0x144f93fba4fab0083a99e432c6258b2e9d70ffb3261a52d3f5c6ae4ea6f48fdf"},
	{1, 6132641, "0xdd74704ca3c8f26e8c4fa4ff045faa11eabd6ed6f4cbb0c7b9e6389521e11453"},
}
