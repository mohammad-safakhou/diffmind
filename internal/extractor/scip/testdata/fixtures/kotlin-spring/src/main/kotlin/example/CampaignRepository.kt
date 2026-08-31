package example

/**
 * CampaignRepository is the data-access layer. The integration test
 * targets `deleteById` as the bottom of the call graph; the walker
 * must reach this from the controller via the service.
 */
class CampaignRepository {
    fun findById(id: String): Campaign? = Campaign(id)
    fun deleteById(id: String) {
        // Stub: production code would delete from a DB.
    }
}
