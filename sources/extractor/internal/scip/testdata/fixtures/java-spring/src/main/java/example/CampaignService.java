package example;

/**
 * CampaignService is the business-logic layer between controller and
 * repository. Its softDelete method is the intermediate hop the
 * walker must follow.
 */
public class CampaignService {

    private final CampaignRepository repo;

    public CampaignService(CampaignRepository repo) {
        this.repo = repo;
    }

    public void softDelete(String id) {
        if (id == null || id.isEmpty()) {
            return;
        }
        Campaign c = repo.findById(id);
        if (c == null) {
            return;
        }
        repo.deleteById(id);
    }
}
