"""CampaignRepository is the data-access layer. The walker integration
test asserts that the path from the handler reaches `find_by_id`."""


class CampaignRepository:
    def __init__(self) -> None:
        self._store: dict[str, str] = {}

    def find_by_id(self, id: str) -> str | None:
        return self._store.get(id)

    def save(self, id: str, value: str) -> None:
        self._store[id] = value
