package example

/**
 * CampaignController is the HTTP entry point. The deleteCampaign
 * method (declared around line 13 of this file) is the symbol the
 * integration test resolves. The walker must trace
 * deleteCampaign → CampaignService.softDelete →
 * CampaignRepository.deleteById.
 */
class CampaignController(private val service: CampaignService) {

    // Anchor for the integration test: deleteCampaign is the entry symbol.
    fun deleteCampaign(id: String?) {
        requireNotNull(id) { "id required" }
        service.softDelete(id)
    }
}
