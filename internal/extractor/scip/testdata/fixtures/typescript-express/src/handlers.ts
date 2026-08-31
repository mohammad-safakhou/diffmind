import { CampaignService } from "./service";

// handlers.ts contains the HTTP entry points. The integration test
// targets `getCampaign` declared on line 10 (1-based) below.

export class CampaignHandlers {
  constructor(private readonly svc: CampaignService) {}

  // Anchor: getCampaign on line 10.
  getCampaign(id: string): string {
    return this.svc.getById(id);
  }

  createCampaign(id: string, value: string): void {
    this.svc.create(id, value);
  }
}
