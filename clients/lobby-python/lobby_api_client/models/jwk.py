from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.jwk_alg import JWKAlg
from ..models.jwk_crv import JWKCrv
from ..models.jwk_kty import JWKKty
from ..models.jwk_use import JWKUse

T = TypeVar("T", bound="JWK")


@_attrs_define
class JWK:
    """
    Attributes:
        alg (JWKAlg): Always EdDSA.
        crv (JWKCrv): Always Ed25519.
        kid (str): The key's ID, which a token's header names as kid.
        kty (JWKKty): Always OKP.
        use (JWKUse): Always sig.
        x (str): The 32-byte public key, base64url without padding.
    """

    alg: JWKAlg
    crv: JWKCrv
    kid: str
    kty: JWKKty
    use: JWKUse
    x: str

    def to_dict(self) -> dict[str, Any]:
        alg = self.alg.value

        crv = self.crv.value

        kid = self.kid

        kty = self.kty.value

        use = self.use.value

        x = self.x

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "alg": alg,
                "crv": crv,
                "kid": kid,
                "kty": kty,
                "use": use,
                "x": x,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        alg = JWKAlg(d.pop("alg"))

        crv = JWKCrv(d.pop("crv"))

        kid = d.pop("kid")

        kty = JWKKty(d.pop("kty"))

        use = JWKUse(d.pop("use"))

        x = d.pop("x")

        jwk = cls(
            alg=alg,
            crv=crv,
            kid=kid,
            kty=kty,
            use=use,
            x=x,
        )

        return jwk
