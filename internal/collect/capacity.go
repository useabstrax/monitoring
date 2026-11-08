package collect

// Capacity is filesystem size and inode usage for one mount.
type Capacity struct {
	Total      uint64
	Used       uint64
	Available  uint64
	InodeTotal uint64
	InodeUsed  uint64
}

func capacityFrom(bsize, blocks, bfree, bavail, files, ffree uint64) Capacity {
	total := blocks * bsize
	avail := bavail * bsize
	free := bfree * bsize
	var used uint64
	if total >= free {
		used = total - free
	}
	var inodeUsed uint64
	if files >= ffree {
		inodeUsed = files - ffree
	}
	return Capacity{Total: total, Used: used, Available: avail, InodeTotal: files, InodeUsed: inodeUsed}
}

func usagePct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
