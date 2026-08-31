"""CampaignService is the intermediate hop in the call graph."""

from .repository import CampaignRepository


class CampaignService:
    def __init__(self, repo: CampaignRepository) -> None:
        self._repo = repo

    def get_by_id(self, id: str) -> str:
        if not id:
            raise ValueError("id required")
        value = self._repo.find_by_id(id)
        if value is None:
            raise LookupError("not found")
        return value

    def create(self, id: str, value: str) -> None:
        if not id or not value:
            raise ValueError("invalid")
        self._repo.save(id, value)
