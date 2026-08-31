import { CampaignRepository } from "./repository";

// CampaignService bridges handlers and the repository. Its methods
// are the intermediate hops the walker must follow.
export class CampaignService {
  constructor(private readonly repo: CampaignRepository) {}

  getById(id: string): string {
    if (!id) {
      throw new Error("id required");
    }
    const value = this.repo.findById(id);
    if (!value) {
      throw new Error("not found");
    }
    return value;
  }

  create(id: string, value: string): void {
    if (!id || !value) {
      throw new Error("invalid");
    }
    this.repo.save(id, value);
  }
}
