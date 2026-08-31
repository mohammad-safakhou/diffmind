namespace ScipFixture;

/// <summary>
/// CampaignController is the HTTP entry point. The
/// <c>DeleteCampaign</c> method (declared around line 14 of this
/// file) is the symbol the integration test resolves. The walker
/// must trace DeleteCampaign → CampaignService.SoftDelete →
/// CampaignRepository.DeleteById.
/// </summary>
public sealed class CampaignController
{
    private readonly CampaignService _service;

    public CampaignController(CampaignService service)
    {
        _service = service;
    }

    // Anchor for the integration test: DeleteCampaign is the entry symbol.
    public void DeleteCampaign(string id)
    {
        ArgumentException.ThrowIfNullOrEmpty(id);
        _service.SoftDelete(id);
    }
}
