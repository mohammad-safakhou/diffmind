namespace ScipFixture;

/// <summary>
/// CampaignRepository is the data-access layer. The integration test
/// targets <c>DeleteById</c> as the bottom of the call graph; the
/// walker must reach this from the controller via the service.
/// </summary>
public sealed class CampaignRepository
{
    public Campaign? FindById(string id) => new Campaign(id);

    public void DeleteById(string id)
    {
        // Stub: production code would issue an SQL DELETE.
    }
}
