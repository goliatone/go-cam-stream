//go:build !linux && !darwin

package viewer

type managedFiles struct{}

func openManagedFiles(string) (*managedFiles, error)    { return nil, storageFailure() }
func (*managedFiles) read(string) ([]byte, bool, error) { return nil, false, storageFailure() }
func (*managedFiles) writeCandidate([]byte) error       { return storageFailure() }
func (*managedFiles) syncFile(string) error             { return storageFailure() }
func (*managedFiles) syncDir() error                    { return storageFailure() }
func (*managedFiles) rename() error                     { return storageFailure() }
func (*managedFiles) remove(string) error               { return storageFailure() }
func (*managedFiles) close() error                      { return nil }

func (*managedFiles) bind(string) error { return storageFailure() }
