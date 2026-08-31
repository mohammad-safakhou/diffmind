package example;

/**
 * CampaignRepository is the data-access layer. The integration test
 * targets `deleteById` as the bottom of the call graph; the walker
 * must reach this from the controller via the service.
 */
public class CampaignRepository {

    public Campaign findById(String id) {
        // Stub implementation: production code would query a DB.
        return new Campaign(id);
    }

    public void deleteById(String id) {
        // Stub: production code would delete from a DB.
    }
}
