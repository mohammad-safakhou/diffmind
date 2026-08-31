package example;

/**
 * CampaignController is the HTTP entry point. The deleteCampaign
 * method on line 20 is the symbol the integration test resolves.
 * The walker must trace deleteCampaign → CampaignService.softDelete →
 * CampaignRepository.deleteById.
 */
public class CampaignController {

    private final CampaignService service;

    public CampaignController(CampaignService service) {
        this.service = service;
    }

    // Anchor for the integration test: deleteCampaign starts on line 20.
    public void deleteCampaign(String id) {
        if (id == null) {
            throw new IllegalArgumentException("id required");
        }
        service.softDelete(id);
    }
}
