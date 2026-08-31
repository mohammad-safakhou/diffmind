namespace ScipFixture;

/// <summary>
/// CampaignService is the intermediate hop in the call graph the
/// walker must follow.
/// </summary>
public sealed class CampaignService
{
    private readonly CampaignRepository _repo;

    public CampaignService(CampaignRepository repo)
    {
        _repo = repo;
    }

    public void SoftDelete(string id)
    {
        if (string.IsNullOrEmpty(id))
        {
            return;
        }
        var campaign = _repo.FindById(id);
        if (campaign is null)
        {
            return;
        }
        _repo.DeleteById(campaign.Id);
    }
}
