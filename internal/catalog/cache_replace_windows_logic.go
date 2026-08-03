package catalog

import "fmt"

type windowsCatalogReplacePrimitives struct {
	targetExists    func(path string) (bool, error)
	replaceExisting func(temporaryPath, finalPath string) error
	moveNew         func(temporaryPath, finalPath string) error
}

func replaceCatalogFileWindows(
	temporaryPath string,
	finalPath string,
	primitives windowsCatalogReplacePrimitives,
) error {
	if err := requireSiblingCatalogPaths(temporaryPath, finalPath); err != nil {
		return err
	}
	if primitives.targetExists == nil || primitives.replaceExisting == nil || primitives.moveNew == nil {
		return fmt.Errorf("Windows catalog replacement primitives are required")
	}
	exists, err := primitives.targetExists(finalPath)
	if err != nil {
		return fmt.Errorf("inspect Windows catalog replacement target: %w", err)
	}
	if exists {
		return primitives.replaceExisting(temporaryPath, finalPath)
	}
	return primitives.moveNew(temporaryPath, finalPath)
}
