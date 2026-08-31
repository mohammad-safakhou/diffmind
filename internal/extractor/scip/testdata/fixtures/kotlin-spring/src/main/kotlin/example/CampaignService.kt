package example

/**
 * CampaignService is the intermediate hop the walker must follow.
 */
class CampaignService(private val repo: CampaignRepository) {

    fun softDelete(id: String) {
        if (id.isEmpty()) return
        val campaign = repo.findById(id) ?: return
        repo.deleteById(campaign.id)
    }
}
