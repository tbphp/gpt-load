package control

import (
	"fmt"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type referencedPrice struct {
	identity       pricing.Identity
	referenceCount int
	groupIDs       map[uint]struct{}
}

func (reference referencedPrice) referenceGroupCount() int {
	return len(reference.groupIDs)
}

type priceReferenceSnapshot struct {
	references map[pricing.Identity]referencedPrice
}

// reconcileReferencedPrices materializes every exact channel/model identity that
// is currently referenced. Existing rows are immutable under ordinary Group
// writes; catalog refresh and broad cleanup are owned by the sync workflow.
func reconcileReferencedPrices(tx *gorm.DB, snapshot *catalog.Snapshot) error {
	references, err := loadReferencedPrices(tx)
	if err != nil {
		return err
	}

	var existing []models.ModelPrice
	if err := tx.Order("id ASC").Find(&existing).Error; err != nil {
		return fmt.Errorf("load existing model prices: %w", app_errors.ParseDBError(err))
	}
	existingIdentities := make(map[pricing.Identity]struct{}, len(existing))
	for _, row := range existing {
		identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
		if err != nil {
			return fmt.Errorf("validate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		if _, duplicate := existingIdentities[identity]; duplicate {
			return fmt.Errorf("duplicate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		existingIdentities[identity] = struct{}{}
	}

	ordered := make([]pricing.Identity, 0, len(references))
	for identity := range references {
		if _, exists := existingIdentities[identity]; !exists {
			ordered = append(ordered, identity)
		}
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].ChannelID != ordered[right].ChannelID {
			return ordered[left].ChannelID < ordered[right].ChannelID
		}
		return ordered[left].ModelID < ordered[right].ModelID
	})
	for _, identity := range ordered {
		reference := references[identity]
		row, err := newReconciledModelPrice(reference, snapshot)
		if err != nil {
			return fmt.Errorf("materialize model price: %w", app_errors.ErrInternalServer)
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("persist reconciled model price: %w", app_errors.ParseDBError(err))
		}
	}
	return nil
}

func loadReferencedPrices(tx *gorm.DB) (map[pricing.Identity]referencedPrice, error) {
	snapshot, err := loadPriceReferenceSnapshot(tx)
	if err != nil {
		return nil, err
	}
	return snapshot.references, nil
}

func loadPriceReferenceSnapshot(tx *gorm.DB) (priceReferenceSnapshot, error) {
	var groups []models.Group
	if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
		return priceReferenceSnapshot{}, fmt.Errorf("load groups for price reconciliation: %w", app_errors.ParseDBError(err))
	}
	return buildPriceReferenceSnapshot(groups)
}

func buildPriceReferenceSnapshot(groups []models.Group) (priceReferenceSnapshot, error) {
	references := make(map[pricing.Identity]referencedPrice)
	for _, group := range groups {
		var groupModels []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
			return priceReferenceSnapshot{}, fmt.Errorf("decode group price references: %w", app_errors.ErrInternalServer)
		}
		for _, model := range groupModels {
			identity, err := PriceIdentityForChannelModel(group.ChannelID, model.ID)
			if err != nil {
				return priceReferenceSnapshot{}, fmt.Errorf("validate group price reference: %w", app_errors.ErrInternalServer)
			}
			reference, exists := references[identity]
			if !exists {
				reference = referencedPrice{identity: identity, groupIDs: make(map[uint]struct{})}
			}
			reference.referenceCount++
			reference.groupIDs[group.ID] = struct{}{}
			references[identity] = reference
		}
	}
	return priceReferenceSnapshot{references: references}, nil
}

func newReconciledModelPrice(
	reference referencedPrice,
	snapshot *catalog.Snapshot,
) (models.ModelPrice, error) {
	row := models.ModelPrice{
		ChannelID: reference.identity.ChannelID,
		ModelID:   reference.identity.ModelID,
		IsManual:  false,
	}
	match, ok := resolveAutomaticPriceForIdentity(snapshot, reference.identity)
	if !ok {
		return row, nil
	}
	desired, err := automaticCatalogValues(match.cost)
	if err != nil {
		return models.ModelPrice{}, err
	}
	row.InputPriceNanoUSDPerMillionTokens = desired.InputPriceNanoUSDPerMillionTokens
	row.OutputPriceNanoUSDPerMillionTokens = desired.OutputPriceNanoUSDPerMillionTokens
	row.CacheReadPriceNanoUSDPerMillionTokens = desired.CacheReadPriceNanoUSDPerMillionTokens
	row.CacheWritePriceNanoUSDPerMillionTokens = desired.CacheWritePriceNanoUSDPerMillionTokens
	row.ContextPriceTiers = desired.ContextPriceTiers
	return row, nil
}

func priceStoragePointer(price pricing.Price) *int64 {
	if !price.Set {
		return nil
	}
	value := int64(price.NanoUSDPerMillion)
	return &value
}
