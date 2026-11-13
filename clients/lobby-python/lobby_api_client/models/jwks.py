from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.jwk import JWK


T = TypeVar("T", bound="JWKS")


@_attrs_define
class JWKS:
    """
    Attributes:
        keys (list[JWK] | None): The lobby's current public key, which signs new player tokens, and any previous ones
            that still verify tokens issued before a rotation.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    keys: list[JWK] | None
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        keys: list[dict[str, Any]] | None
        if isinstance(self.keys, list):
            keys = []
            for keys_type_0_item_data in self.keys:
                keys_type_0_item = keys_type_0_item_data.to_dict()
                keys.append(keys_type_0_item)

        else:
            keys = self.keys

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "keys": keys,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.jwk import JWK

        d = dict(src_dict)

        def _parse_keys(data: object) -> list[JWK] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                keys_type_0 = []
                _keys_type_0 = data
                for keys_type_0_item_data in _keys_type_0:
                    keys_type_0_item = JWK.from_dict(keys_type_0_item_data)

                    keys_type_0.append(keys_type_0_item)

                return keys_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[JWK] | None, data)

        keys = _parse_keys(d.pop("keys"))

        schema = d.pop("$schema", UNSET)

        jwks = cls(
            keys=keys,
            schema=schema,
        )

        return jwks
