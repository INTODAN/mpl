from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.watcher import Watcher


T = TypeVar("T", bound="WatcherList")


@_attrs_define
class WatcherList:
    """
    Attributes:
        watchers (list[Watcher]): The watchers the host hasn't kicked, in the order they started watching.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    watchers: list[Watcher]
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        watchers = []
        for watchers_item_data in self.watchers:
            watchers_item = watchers_item_data.to_dict()
            watchers.append(watchers_item)

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "watchers": watchers,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.watcher import Watcher

        d = dict(src_dict)
        watchers = []
        _watchers = d.pop("watchers")
        for watchers_item_data in _watchers:
            watchers_item = Watcher.from_dict(watchers_item_data)

            watchers.append(watchers_item)

        schema = d.pop("$schema", UNSET)

        watcher_list = cls(
            watchers=watchers,
            schema=schema,
        )

        return watcher_list
