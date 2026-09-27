# Complete MCP tool inventory

AUDITED COMMIT: b95da59689cdacb544c659c5c0c1fcbecc50a995

156 tools from real stdio ListTools, matched against TOOLS and dispatch. JSON schemas below are upstream declarations, not proof of semantic validation. Full machine-readable map: [tools-map.json](tools-map.json).

{'READ_SENSITIVE': 48, 'SAFE_READ': 51, 'WRITE': 36, 'DESTRUCTIVE_OR_HIGH_RISK': 21}

## ozon_list_shops

Registered Ozon shops (магазины): shop_id + name. Use shop_id in all other tools.

Classification: **READ_SENSITIVE**. Family: Local. Credentials: None for local tools.

Dispatch: `ozon_mcp/server.py:951`.


Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {}
}
```

## ozon_actions_list

[P0] Ozon promotions available now and which goods may be pulled in (акции). Goods in promos can sell below cost.

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:996`.

- `ozon_mcp/client.py:57` `OzonSellerClient.actions_list`
  - `GET https://api-seller.ozon.ru/v1/actions` (client.py:59)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_actions_candidates

[P0] Candidate goods Ozon PLANS to pull into a promotion; pre-emptive check against selling at a loss (кандидаты в акцию).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:998`.

- `ozon_mcp/client.py:61` `OzonSellerClient.actions_candidates`
  - `POST https://api-seller.ozon.ru/v1/actions/candidates` (client.py:66)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer",
      "description": "action id from ozon_actions_list"
    }
  },
  "required": [
    "action_id"
  ]
}
```

## ozon_actions_products

[P0] Goods already participating in a promotion, sold at the promo price (товары в акции).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1000`.

- `ozon_mcp/client.py:68` `OzonSellerClient.actions_products`
  - `POST https://api-seller.ozon.ru/v1/actions/products` (client.py:73)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer",
      "description": "action id"
    }
  },
  "required": [
    "action_id"
  ]
}
```

## ozon_actions_activate

[P0] Add goods to a promotion at a given promo price (вступить в акцию).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1002`.

- `ozon_mcp/client.py:75` `OzonSellerClient.actions_products_activate`
  - `POST https://api-seller.ozon.ru/v1/actions/products/activate` (client.py:79)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "products": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "product_id": {
            "type": "integer"
          },
          "action_price": {
            "type": "number"
          }
        }
      }
    }
  },
  "required": [
    "action_id",
    "products"
  ]
}
```

## ozon_actions_deactivate

[P0] Remove goods from a promotion, e.g. loss-making ones (выйти из акции).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1004`.

- `ozon_mcp/client.py:84` `OzonSellerClient.actions_products_deactivate`
  - `POST https://api-seller.ozon.ru/v1/actions/products/deactivate` (client.py:86)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "product_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "action_id",
    "product_ids"
  ]
}
```

## ozon_seller_actions

Seller's own promotions, as opposed to Ozon's (собственные акции).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1008`.

- `ozon_mcp/client.py:92` `OzonSellerClient.seller_actions_list`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/list` (client.py:97)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "status": {
      "type": "string",
      "description": "status filter"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_seller_action_create

Create the seller's own discount promotion (создать акцию).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1010`.

- `ozon_mcp/client.py:99` `OzonSellerClient.seller_action_create_discount`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/create/discount` (client.py:102)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "title": {
      "type": "string"
    },
    "date_start": {
      "type": "string",
      "description": "RFC3339"
    },
    "date_end": {
      "type": "string"
    },
    "min_action_percent": {
      "type": "integer",
      "description": "min discount %"
    }
  },
  "required": [
    "title",
    "date_start",
    "date_end",
    "min_action_percent"
  ]
}
```

## ozon_seller_action_toggle

Enable/disable the seller's own promotion (включить акцию).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1014`.

- `ozon_mcp/client.py:107` `OzonSellerClient.seller_action_toggle`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/change-activity` (client.py:109)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "is_turn_on": {
      "type": "boolean"
    }
  },
  "required": [
    "action_id",
    "is_turn_on"
  ]
}
```

## ozon_seller_action_products

Goods in the seller's own promotion (товары акции).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1016`.

- `ozon_mcp/client.py:112` `OzonSellerClient.seller_action_products`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/products/list` (client.py:117)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  },
  "required": [
    "action_id"
  ]
}
```

## ozon_seller_action_products_add

Add goods to the seller's own promotion (добавить товары).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1018`.

- `ozon_mcp/client.py:119` `OzonSellerClient.seller_action_products_add`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/products/add` (client.py:121)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "products": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "[{product_id, action_price}, ...]"
    }
  },
  "required": [
    "action_id",
    "products"
  ]
}
```

## ozon_seller_action_products_delete

Remove goods from the seller's own promotion (убрать товары).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1020`.

- `ozon_mcp/client.py:124` `OzonSellerClient.seller_action_products_delete`
  - `POST https://api-seller.ozon.ru/v1/seller-actions/products/delete` (client.py:126)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "product_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "action_id",
    "product_ids"
  ]
}
```

## ozon_pricing_strategy_list

Pricing strategies: auto price management against competitors (ценовые стратегии).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1024`.

- `ozon_mcp/client.py:130` `OzonSellerClient.pricing_strategy_list`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/list` (client.py:132)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "page": {
      "type": "integer",
      "default": 1
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_pricing_strategy_create

Create a pricing strategy. competitors: [{competitor_id, coefficient}], coefficient 0.5-1.2 of the competitor price (создать стратегию).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1026`.

- `ozon_mcp/client.py:134` `OzonSellerClient.pricing_strategy_create`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/create` (client.py:139)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "name": {
      "type": "string"
    },
    "competitors": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "name",
    "competitors"
  ]
}
```

## ozon_pricing_strategy_info

Pricing strategy details (детали стратегии).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1028`.

- `ozon_mcp/client.py:142` `OzonSellerClient.pricing_strategy_info`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/info` (client.py:144)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "strategy_id": {
      "type": "string"
    }
  },
  "required": [
    "strategy_id"
  ]
}
```

## ozon_pricing_strategy_update

Update a pricing strategy (обновить стратегию).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1030`.

- `ozon_mcp/client.py:146` `OzonSellerClient.pricing_strategy_update`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/update` (client.py:148)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "strategy_id": {
      "type": "string"
    },
    "name": {
      "type": "string"
    },
    "competitors": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "strategy_id",
    "name",
    "competitors"
  ]
}
```

## ozon_pricing_strategy_delete

Delete a pricing strategy (удалить стратегию).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1032`.

- `ozon_mcp/client.py:151` `OzonSellerClient.pricing_strategy_delete`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/delete` (client.py:153)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "strategy_id": {
      "type": "string"
    }
  },
  "required": [
    "strategy_id"
  ]
}
```

## ozon_pricing_strategy_status

Enable/disable a pricing strategy (статус стратегии).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1034`.

- `ozon_mcp/client.py:155` `OzonSellerClient.pricing_strategy_status`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/status` (client.py:157)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "strategy_id": {
      "type": "string"
    },
    "enabled": {
      "type": "boolean"
    }
  },
  "required": [
    "strategy_id",
    "enabled"
  ]
}
```

## ozon_pricing_strategy_products

Strategy goods: action=list | add | delete (товары стратегии).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1036`.

- `ozon_mcp/client.py:160` `OzonSellerClient.pricing_strategy_products_add`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/products/add` (client.py:162)
- `ozon_mcp/client.py:165` `OzonSellerClient.pricing_strategy_products_delete`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/products/delete` (client.py:167)
- `ozon_mcp/client.py:169` `OzonSellerClient.pricing_strategy_products_list`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/products/list` (client.py:171)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Mixed tool: action=list reads, add/delete writes; classify entire capability as WRITE

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action": {
      "type": "string",
      "description": "list | add | delete"
    },
    "strategy_id": {
      "type": "string",
      "description": "for list/add"
    },
    "product_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "for add/delete"
    }
  },
  "required": [
    "action"
  ]
}
```

## ozon_pricing_competitors

Competitors from other marketplaces, input for pricing strategies (конкуренты).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1043`.

- `ozon_mcp/client.py:173` `OzonSellerClient.pricing_competitors_list`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/competitors/list` (client.py:175)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "page": {
      "type": "integer",
      "default": 1
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_pricing_competitor_prices

Competitor price for goods in strategies (цены конкурентов).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1045`.

- `ozon_mcp/client.py:177` `OzonSellerClient.pricing_competitor_price`
  - `POST https://api-seller.ozon.ru/v1/pricing-strategy/product/info` (client.py:179)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "integer"
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_set_prices

[P0] Set prices. Use min_price to block promos below cost (установить цены).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1049`.

- `ozon_mcp/client.py:182` `OzonSellerClient.product_import_prices`
  - `POST https://api-seller.ozon.ru/v1/product/import/prices` (client.py:194)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "prices": {
      "type": "array",
      "description": "[{offer_id, price, old_price, min_price, auto_action_enabled}]",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "prices"
  ]
}
```

## ozon_get_prices

[P0] Current prices, discounts, min price and price index. price_index over 1.15 risks quarantine (цены, индекс цен).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1051`.

- `ozon_mcp/client.py:202` `OzonSellerClient.product_info_prices`
  - `POST https://api-seller.ozon.ru/v5/product/info/prices` (client.py:216)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_id": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "offer_id filter"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "product_id filter"
    },
    "limit": {
      "type": "integer",
      "default": 100
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_get_prices_v4

Prices via v4 API, includes purchase_price/cost (цены v4, себестоимость).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1057`.

- `ozon_mcp/client.py:196` `OzonSellerClient.product_info_prices_v4`
  - `POST https://api-seller.ozon.ru/v5/product/info/prices` (client.py:216)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_id": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "offer_id filter"
    },
    "limit": {
      "type": "integer",
      "default": 100
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_min_price_timer_status

[P0] Min price timer status, 30 days. Expired means goods are exposed to promos below cost (таймер минимальной цены).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1062`.

- `ozon_mcp/client.py:218` `OzonSellerClient.action_timer_status`
  - `POST https://api-seller.ozon.ru/v1/product/action/timer/status` (client.py:220)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_min_price_timer_renew

[P0] Renew the min price timer for 30 days (продлить таймер).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1064`.

- `ozon_mcp/client.py:224` `OzonSellerClient.action_timer_update`
  - `POST https://api-seller.ozon.ru/v1/product/action/timer/update` (client.py:226)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_finance_transactions

[P0] Financial operations: commissions, logistics, storage, returns (транзакции, расходы). Built from daily accruals — Ozon retired the period endpoint; max 31 days per call.

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1068`.

- `ozon_mcp/client.py:237` `OzonSellerClient.finance_transaction_list`
  - `POST https://api-seller.ozon.ru/v1/finance/accrual/by-day` (client.py:438)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string",
      "description": "YYYY-MM-DDT00:00:00Z"
    },
    "date_to": {
      "type": "string"
    },
    "page": {
      "type": "integer",
      "default": 1
    },
    "page_size": {
      "type": "integer",
      "default": 50
    },
    "operation_type": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "type filter"
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  },
  "required": [
    "date_from",
    "date_to"
  ]
}
```

## ozon_finance_totals

[P0] Period totals by accrual category and service type_id (итоги финансов). Recomputed from daily accruals — Ozon retired the totals endpoint; max 31 days per call.

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1074`.

- `ozon_mcp/client.py:270` `OzonSellerClient.finance_transaction_totals`
  - `POST https://api-seller.ozon.ru/v1/finance/accrual/types` (client.py:390)
  - `POST https://api-seller.ozon.ru/v1/finance/accrual/by-day` (client.py:438)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "date_from",
    "date_to"
  ]
}
```

## ozon_finance_realization

Monthly realization report, v2 (отчёт о реализации).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1076`.

- `ozon_mcp/client.py:374` `OzonSellerClient.finance_realization`
  - `POST https://api-seller.ozon.ru/v2/finance/realization` (client.py:376)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "month": {
      "type": "integer",
      "description": "1-12"
    },
    "year": {
      "type": "integer"
    }
  },
  "required": [
    "month",
    "year"
  ]
}
```

## ozon_finance_mutual_settlement

Monthly mutual settlement report (взаиморасчёты).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1078`.

- `ozon_mcp/client.py:378` `OzonSellerClient.finance_mutual_settlement`
  - `POST https://api-seller.ozon.ru/v1/finance/mutual-settlement` (client.py:380)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date": {
      "type": "string",
      "description": "YYYY-MM"
    }
  },
  "required": [
    "date"
  ]
}
```

## ozon_finance_accruals

Daily accruals (начисления).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1080`.

- `ozon_mcp/client.py:429` `OzonSellerClient.finance_accrual_by_day`
  - `POST https://api-seller.ozon.ru/v1/finance/accrual/by-day` (client.py:438)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date": {
      "type": "string",
      "description": "YYYY-MM-DD"
    }
  },
  "required": [
    "date"
  ]
}
```

## ozon_finance_balance

[P0] Seller balance for a period: opening/closing, accruals, payouts (Beta). No dates = last 30 days (баланс).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1082`.

- `ozon_mcp/client.py:562` `OzonSellerClient.finance_balance`
  - `POST https://api-seller.ozon.ru/v1/finance/balance` (client.py:572)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "date_to": {
      "type": "string",
      "description": "YYYY-MM-DD"
    }
  }
}
```

## ozon_finance_cash_flow

Cash flow (движение средств).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1085`.

- `ozon_mcp/client.py:444` `OzonSellerClient.finance_cash_flow`
  - `POST https://api-seller.ozon.ru/v1/finance/cash-flow-statement/list` (client.py:448)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "date_from",
    "date_to"
  ]
}
```

## ozon_rating_summary

[P0] Seller rating; drives search position, promo access and storage cost (рейтинг продавца).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1089`.

- `ozon_mcp/client.py:455` `OzonSellerClient.rating_summary`
  - `POST https://api-seller.ozon.ru/v1/rating/summary` (client.py:457)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_rating_history

Seller rating history (история рейтинга).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1091`.

- `ozon_mcp/client.py:459` `OzonSellerClient.rating_history`
  - `POST https://api-seller.ozon.ru/v1/rating/history` (client.py:461)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "date_from",
    "date_to"
  ]
}
```

## ozon_reviews

[P0] Product reviews; negatives cut conversion (отзывы).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1095`.

- `ozon_mcp/client.py:467` `OzonSellerClient.review_list`
  - `POST https://api-seller.ozon.ru/v1/review/list` (client.py:477)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "sku": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "SKU filter"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_review_reply

Reply to a review (ответить на отзыв).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1100`.

- `ozon_mcp/client.py:479` `OzonSellerClient.review_comment_create`
  - `POST https://api-seller.ozon.ru/v1/review/comment/create` (client.py:481)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "review_id": {
      "type": "string"
    },
    "text": {
      "type": "string"
    }
  },
  "required": [
    "review_id",
    "text"
  ]
}
```

## ozon_review_comments

Review comments. Ozon has no edit-reply method: delete and create again (комментарии к отзыву).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1102`.

- `ozon_mcp/client.py:483` `OzonSellerClient.review_comment_list`
  - `POST https://api-seller.ozon.ru/v1/review/comment/list` (client.py:485)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "review_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 20
    }
  },
  "required": [
    "review_id"
  ]
}
```

## ozon_review_reply_delete

Delete a review reply (удалить ответ).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1104`.

- `ozon_mcp/client.py:491` `OzonSellerClient.review_comment_delete`
  - `POST https://api-seller.ozon.ru/v1/review/comment/delete` (client.py:493)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "review_id": {
      "type": "string"
    },
    "comment_id": {
      "type": "string"
    }
  },
  "required": [
    "review_id",
    "comment_id"
  ]
}
```

## ozon_ad_campaigns

[P0] Ad campaigns: budgets in micro-rubles (1000000 = 1₽), statuses. adv_object_type: SKU | SEARCH_PROMO | BANNER. state: CAMPAIGN_STATE_RUNNING | _STOPPED | _INACTIVE (реклама, кампании).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1108`.

- `ozon_mcp/client.py:1215` `OzonPerformanceClient.campaigns_list`
  - `GET https://api-performance.ozon.ru/api/client/campaign` (client.py:1232)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "filter"
    },
    "adv_object_type": {
      "type": "string",
      "description": "type filter"
    },
    "state": {
      "type": "string",
      "description": "status filter"
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_ad_statistics

[P0] Campaign statistics, async Ozon report, up to ~2 min. Limits: ≤10 campaigns, ≤62 days, one report at a time (статистика рекламы).

Classification: **WRITE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1115`.

- `ozon_mcp/client.py:1348` `OzonPerformanceClient.statistics`
  - `POST https://api-performance.ozon.ru/api/client/statistics/json` (client.py:1357)
  - `GET https://api-performance.ozon.ru/api/client/statistics/{uuid}` (client.py:1367)
  - `POST https://api-performance.ozon.ru/api/client/token` (client.py:1183)
  - `GET https://api-performance.ozon.ru/api/client/statistics/report` (client.py:1370)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaigns": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "campaign ids"
    },
    "date_from": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "date_to": {
      "type": "string"
    },
    "group_by": {
      "type": "string",
      "default": "DATE"
    }
  },
  "required": [
    "campaigns",
    "date_from",
    "date_to"
  ]
}
```

## ozon_ad_campaign_stop

[P0] Emergency stop of an ad campaign (остановить рекламу).

Classification: **WRITE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1121`.

- `ozon_mcp/client.py:1281` `OzonPerformanceClient.campaign_deactivate`
  - `POST https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/deactivate` (client.py:1283)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_campaign_objects

Goods and bids inside an ad campaign (товары и ставки).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1124`.

- `ozon_mcp/client.py:1322` `OzonPerformanceClient.campaign_objects`
  - `GET https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/objects` (client.py:1324)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_campaign_create

Create a CPC Trafarety campaign, the only type creatable via API. placement: PLACEMENT_SEARCH_AND_CATEGORY | PLACEMENT_TOP_PROMOTION. strategy: MAX_CLICKS | TOP_MAX_CLICKS | TARGET_BIDS | TOP_PROMOTION | NO_AUTO_STRATEGY. Min budget 2000₽ per SKU; add goods via ozon_ad_products_add (создать кампанию).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1127`.

- `ozon_mcp/client.py:1234` `OzonPerformanceClient.campaign_create`
  - `POST https://api-performance.ozon.ru/api/client/campaign/cpc/v2/product` (client.py:1257)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "title": {
      "type": "string"
    },
    "placement": {
      "type": "string",
      "default": "PLACEMENT_SEARCH_AND_CATEGORY"
    },
    "strategy": {
      "type": "string",
      "default": "MAX_CLICKS"
    },
    "daily_budget_rub": {
      "type": "number",
      "description": "daily budget, RUB"
    },
    "weekly_budget_rub": {
      "type": "number",
      "description": "weekly budget, RUB"
    }
  },
  "required": [
    "title"
  ]
}
```

## ozon_ad_campaign_activate

Start an ad campaign (запустить кампанию).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1136`.

- `ozon_mcp/client.py:1277` `OzonPerformanceClient.campaign_activate`
  - `POST https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/activate` (client.py:1279)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_campaign_bids

[P0] Update goods bids in a campaign. bids: [{sku, bid}], bid in MICRO-RUBLES as a string (10000000 = 10₽) (ставки).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1139`.

- `ozon_mcp/client.py:1297` `OzonPerformanceClient.campaign_products_update`
  - `PUT https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/products` (client.py:1299)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "bids": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "campaign_id",
    "bids"
  ]
}
```

## ozon_ad_campaign_budget

Campaign budget, taken from the campaign list; Ozon has no separate endpoint (бюджет кампании).

Classification: **READ_SENSITIVE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1142`.

- `ozon_mcp/client.py:1215` `OzonPerformanceClient.campaigns_list`
  - `GET https://api-performance.ozon.ru/api/client/campaign` (client.py:1232)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_campaign_budget_update

Change campaign budget or period (PATCH). Budgets in RUBLES (изменить бюджет).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1145`.

- `ozon_mcp/client.py:1259` `OzonPerformanceClient.campaign_update`
  - `POST https://api-performance.ozon.ru/api/client/token` (client.py:1183)
  - `PATCH https://api-performance.ozon.ru/api/client/campaign/{campaign_id}` (client.py:1273)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "daily_budget_rub": {
      "type": "number"
    },
    "weekly_budget_rub": {
      "type": "number"
    },
    "from_date": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "to_date": {
      "type": "string",
      "description": "YYYY-MM-DD"
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_campaign_products

Goods and bids in a campaign (товары кампании).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1154`.

- `ozon_mcp/client.py:1285` `OzonPerformanceClient.campaign_products_list`
  - `GET https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/v2/products` (client.py:1287)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "page": {
      "type": "integer",
      "default": 1
    }
  },
  "required": [
    "campaign_id"
  ]
}
```

## ozon_ad_products_add

Add goods to a CPC campaign, max 500. bids: [{sku, bid}] in micro-rubles; without bid the competitive bid applies (добавить товары).

Classification: **WRITE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1157`.

- `ozon_mcp/client.py:1290` `OzonPerformanceClient.campaign_products_add`
  - `POST https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/products` (client.py:1295)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "bids": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "campaign_id",
    "bids"
  ]
}
```

## ozon_ad_products_delete

Remove goods from a campaign (убрать товары).

Classification: **WRITE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1160`.

- `ozon_mcp/client.py:1301` `OzonPerformanceClient.campaign_products_delete`
  - `POST https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/products/delete` (client.py:1303)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "campaign_id",
    "skus"
  ]
}
```

## ozon_ad_bids_competitive

Competitive bids by SKU in a campaign, max 200 (конкурентные ставки).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1163`.

- `ozon_mcp/client.py:1306` `OzonPerformanceClient.bids_competitive`
  - `GET https://api-performance.ozon.ru/api/client/campaign/{campaign_id}/products/bids/competitive` (client.py:1308)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaign_id": {
      "type": "integer"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "campaign_id",
    "skus"
  ]
}
```

## ozon_ad_min_bids

Minimum bids by SKU. payment_type: CPC | CPO | CPC_TOP (минимальные ставки).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1166`.

- `ozon_mcp/client.py:1311` `OzonPerformanceClient.min_sku_bids`
  - `POST https://api-performance.ozon.ru/api/client/min/sku` (client.py:1313)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "payment_type": {
      "type": "string",
      "default": "CPC"
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_search_promo_products

[P0] Goods in pay-per-order top promotion (CPO): bid %, visibility. Bids fixed since 02.2025 (оплата за заказ).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1169`.

- `ozon_mcp/client.py:1329` `OzonPerformanceClient.search_promo_products`
  - `POST https://api-performance.ozon.ru/api/client/campaign/search_promo/v2/products` (client.py:1331)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "page": {
      "type": "integer",
      "default": 1
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_search_promo_enable

Enable pay-per-order promotion for goods, max 1000 SKU (включить продвижение).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1172`.

- `ozon_mcp/client.py:1334` `OzonPerformanceClient.search_promo_enable`
  - `POST https://api-performance.ozon.ru/api/client/search_promo/product/enable` (client.py:1336)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_search_promo_disable

[P0] Disable pay-per-order promotion, max 1000 SKU; use when ДРР is high (отключить продвижение).

Classification: **WRITE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1175`.

- `ozon_mcp/client.py:1338` `OzonPerformanceClient.search_promo_disable`
  - `POST https://api-performance.ozon.ru/api/client/search_promo/product/disable` (client.py:1340)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_search_promo_bids

Fixed CPO bids by SKU, max 200 (ставки CPO).

Classification: **SAFE_READ**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1178`.

- `ozon_mcp/client.py:1342` `OzonPerformanceClient.search_promo_cpo_bids`
  - `POST https://api-performance.ozon.ru/api/client/search_promo/get_cpo_min_bids` (client.py:1344)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_ad_statistics_daily

Daily ad statistics (ежедневная статистика).

Classification: **READ_SENSITIVE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1184`.

- `ozon_mcp/client.py:1381` `OzonPerformanceClient.statistics_daily`
  - `GET https://api-performance.ozon.ru/api/client/statistics/daily/json` (client.py:1386)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaigns": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "campaigns",
    "date_from",
    "date_to"
  ]
}
```

## ozon_ad_statistics_expenses

Ad campaign spend (расходы на рекламу).

Classification: **READ_SENSITIVE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1187`.

- `ozon_mcp/client.py:1388` `OzonPerformanceClient.statistics_expenses`
  - `GET https://api-performance.ozon.ru/api/client/statistics/expense/json` (client.py:1393)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaigns": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "campaigns",
    "date_from",
    "date_to"
  ]
}
```

## ozon_ad_statistics_products

[P0] CPC campaign stats per product: spend, CTR, CPC, orders, ДРР. Synchronous (статистика по товарам).

Classification: **READ_SENSITIVE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1181`.

- `ozon_mcp/client.py:1395` `OzonPerformanceClient.statistics_products`
  - `GET https://api-performance.ozon.ru/api/client/statistics/campaign/product/json` (client.py:1397)
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "campaigns": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "campaigns",
    "date_from",
    "date_to"
  ]
}
```

## ozon_ad_balance

Ad account balance; no official method, see spend in ozon_ad_statistics_expenses (баланс рекламы).

Classification: **READ_SENSITIVE**. Family: Performance API. Credentials: Performance client_id + client_secret => Bearer; current dispatch additionally requires Seller credentials.

Dispatch: `ozon_mcp/server.py:1190`.

- `ozon_mcp/client.py:1403` `OzonPerformanceClient.balance`
- Auth prerequisite: `POST https://api-performance.ozon.ru/api/client/token (conditional token refresh)`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Local unsupported-endpoint stub; no data request, not a working read capability

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_analytics

Analytics by SKU. Funnel metrics (session_view, hits_view, position_category) are deprecated by Ozon; trade metrics work: revenue, ordered_units, delivered_units, returns, cancellations. For search positions use ozon_product_queries (аналитика).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1195`.

- `ozon_mcp/client.py:496` `OzonSellerClient.analytics_data`
  - `POST https://api-seller.ozon.ru/v1/analytics/data` (client.py:513)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    },
    "metrics": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "revenue, ordered_units, delivered_units, returns, cancellations"
    },
    "dimensions": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "sku, day, week, month"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  },
  "required": [
    "date_from",
    "date_to",
    "metrics",
    "dimensions"
  ]
}
```

## ozon_stock_on_warehouses

Stock and turnover at Ozon warehouses, via turnover/stocks — the old endpoint was removed (остатки на складах).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1201`.

- `ozon_mcp/client.py:530` `OzonSellerClient.analytics_stock_on_warehouses`
  - `POST https://api-seller.ozon.ru/v1/analytics/turnover/stocks` (client.py:528)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 100
    },
    "offset": {
      "type": "integer",
      "default": 0
    }
  }
}
```

## ozon_analytics_stocks

Stock analytics for specific goods: availability, scarcity, liquidity, 1-100 SKU (аналитика остатков).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1206`.

- `ozon_mcp/client.py:518` `OzonSellerClient.analytics_stocks`
  - `POST https://api-seller.ozon.ru/v1/analytics/stocks` (client.py:520)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "SKUs, 1-100"
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_product_queries

[P0] Search queries and positions of my goods in Ozon search (Premium). Visibility drives sales (поисковые запросы, позиции).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1431`.

- `ozon_mcp/client.py:548` `OzonSellerClient.product_queries_details`
  - `POST https://api-seller.ozon.ru/v1/analytics/product-queries/details` (client.py:553)
- `ozon_mcp/client.py:536` `OzonSellerClient.product_queries`
  - `POST https://api-seller.ozon.ru/v1/analytics/product-queries` (client.py:544)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "details": {
      "type": "boolean",
      "default": false,
      "description": "true = per-query detail"
    }
  },
  "required": [
    "date_from",
    "skus"
  ]
}
```

## ozon_search_queries_top

Popular Ozon search queries, input for card SEO (топ запросов).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1435`.

- `ozon_mcp/client.py:558` `OzonSellerClient.search_queries_top`
  - `POST https://api-seller.ozon.ru/v1/search-queries/top` (client.py:560)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_supply_orders

FBO supply orders (v3), returns order_ids; details via ozon_supply_order_get (заявки на поставку).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1437`.

- `ozon_mcp/client.py:891` `OzonSellerClient.supply_orders_list`
  - `POST https://api-seller.ozon.ru/v3/supply-order/list` (client.py:897)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "states": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "status codes 1-8, default all"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_supply_order_get

FBO supply order details, 1-50 (детали поставки).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1439`.

- `ozon_mcp/client.py:902` `OzonSellerClient.supply_orders_get`
  - `POST https://api-seller.ozon.ru/v3/supply-order/get` (client.py:904)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "order_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "order_ids"
  ]
}
```

## ozon_supply_order_counters

Supply order counters by status (счётчики поставок).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1441`.

- `ozon_mcp/client.py:906` `OzonSellerClient.supply_order_status_counter`
  - `POST https://api-seller.ozon.ru/v1/supply-order/status/counter` (client.py:908)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_supply_order_timeslots

Available FBO supply timeslots (таймслоты).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1443`.

- `ozon_mcp/client.py:910` `OzonSellerClient.supply_order_timeslots`
  - `POST https://api-seller.ozon.ru/v1/supply-order/timeslot/get` (client.py:912)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "supply_order_id": {
      "type": "integer"
    }
  },
  "required": [
    "supply_order_id"
  ]
}
```

## ozon_product_list

All goods. visibility: ALL, VISIBLE, QUARANTINE, ARCHIVED and others (список товаров).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1210`.

- `ozon_mcp/client.py:593` `OzonSellerClient.product_list`
  - `POST https://api-seller.ozon.ru/v3/product/list` (client.py:598)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "visibility": {
      "type": "string",
      "default": "ALL",
      "description": "ALL, VISIBLE, QUARANTINE, ARCHIVED..."
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_product_info

Extended product info (информация о товарах).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1215`.

- `ozon_mcp/client.py:603` `OzonSellerClient.product_info_list`
  - `POST https://api-seller.ozon.ru/v3/product/info/list` (client.py:605)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_product_attributes

Product attributes including BRAND (атрибуты, бренд).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1217`.

- `ozon_mcp/client.py:609` `OzonSellerClient.product_info_attributes`
  - `POST https://api-seller.ozon.ru/v4/product/info/attributes` (client.py:622)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_id": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "offer_id filter"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "id filter"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_product_stocks

Product stock at FBO/FBS warehouses (остатки товаров).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1223`.

- `ozon_mcp/client.py:624` `OzonSellerClient.product_info_stocks`
  - `POST https://api-seller.ozon.ru/v4/product/info/stocks` (client.py:637)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_id": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_product_certificates

Product certificates; an expired one blocks the card (сертификаты).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1229`.

- `ozon_mcp/client.py:639` `OzonSellerClient.product_certificate_list`
  - `POST https://api-seller.ozon.ru/v1/product/certificate/list` (client.py:643)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_product_import

Create/update goods, bulk import (импорт товаров).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1233`.

- `ozon_mcp/client.py:648` `OzonSellerClient.product_import`
  - `POST https://api-seller.ozon.ru/v3/product/import` (client.py:650)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "items": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "goods to import"
    }
  },
  "required": [
    "items"
  ]
}
```

## ozon_product_import_info

Product import task status (статус импорта).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1235`.

- `ozon_mcp/client.py:652` `OzonSellerClient.product_import_info`
  - `POST https://api-seller.ozon.ru/v1/product/import/info` (client.py:654)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "task_id": {
      "type": "integer"
    }
  },
  "required": [
    "task_id"
  ]
}
```

## ozon_product_update_offer_id

Update product offer IDs (артикулы).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1237`.

- `ozon_mcp/client.py:656` `OzonSellerClient.product_update_offer_id`
  - `POST https://api-seller.ozon.ru/v1/product/update/offer-id` (client.py:658)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "update_offer_id": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "update_offer_id"
  ]
}
```

## ozon_product_update_images

Update product images (изображения).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1239`.

- `ozon_mcp/client.py:660` `OzonSellerClient.product_update_images`
  - `POST https://api-seller.ozon.ru/v1/product/pictures/import` (client.py:662)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "integer"
    },
    "images": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  },
  "required": [
    "product_id",
    "images"
  ]
}
```

## ozon_product_description

Product description by offer_id (описание товара).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1241`.

- `ozon_mcp/client.py:664` `OzonSellerClient.product_info_description`
  - `POST https://api-seller.ozon.ru/v1/product/info/description` (client.py:666)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_id": {
      "type": "string"
    }
  },
  "required": [
    "offer_id"
  ]
}
```

## ozon_product_update_stocks

Update FBS stock (обновить остатки).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1243`.

- `ozon_mcp/client.py:668` `OzonSellerClient.product_update_stocks`
  - `POST https://api-seller.ozon.ru/v2/products/stocks` (client.py:670)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "stocks": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "offer_id/product_id, stock, warehouse_id"
    }
  },
  "required": [
    "stocks"
  ]
}
```

## ozon_product_unarchive

Restore goods from archive (вернуть из архива).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1245`.

- `ozon_mcp/client.py:672` `OzonSellerClient.product_unarchive`
  - `POST https://api-seller.ozon.ru/v1/product/unarchive` (client.py:674)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_product_delete

Delete goods without SKU from archive, by offer_id (удалить товары).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1247`.

- `ozon_mcp/client.py:676` `OzonSellerClient.product_delete`
  - `POST https://api-seller.ozon.ru/v2/products/delete` (client.py:678)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "offer_ids": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  },
  "required": [
    "offer_ids"
  ]
}
```

## ozon_product_limits

Product creation limits (лимиты товаров).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1256`.

- `ozon_mcp/client.py:680` `OzonSellerClient.product_info_limit`
  - `POST https://api-seller.ozon.ru/v4/product/info/limit` (client.py:682)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_product_rating_by_sku

Content rating of goods (рейтинг контента).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1258`.

- `ozon_mcp/client.py:684` `OzonSellerClient.product_rating_by_sku`
  - `POST https://api-seller.ozon.ru/v1/product/rating-by-sku` (client.py:686)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "skus"
  ]
}
```

## ozon_product_discounted

Markdown info for discounted SKUs (уценённые товары).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1260`.

- `ozon_mcp/client.py:688` `OzonSellerClient.product_info_discounted`
  - `POST https://api-seller.ozon.ru/v1/product/info/discounted` (client.py:690)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "discounted_skus": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "discounted_skus"
  ]
}
```

## ozon_product_attributes_update

Update product characteristics without a full card re-upload (обновить характеристики).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1249`.

- `ozon_mcp/client.py:574` `OzonSellerClient.product_attributes_update`
  - `POST https://api-seller.ozon.ru/v1/product/attributes/update` (client.py:576)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "items": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "[{offer_id, attributes: [{id, values}]}]"
    }
  },
  "required": [
    "items"
  ]
}
```

## ozon_product_import_by_sku

Create a copy product from an existing Ozon SKU (копия товара).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1251`.

- `ozon_mcp/client.py:578` `OzonSellerClient.product_import_by_sku`
  - `POST https://api-seller.ozon.ru/v1/product/import-by-sku` (client.py:580)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "items": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "[{sku, name, offer_id, price, old_price, vat, currency_code}]"
    }
  },
  "required": [
    "items"
  ]
}
```

## ozon_product_stocks_by_warehouse

FBS stock per warehouse (v2; v1 is switched off 2026-04-07) (остатки по складам).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1253`.

- `ozon_mcp/client.py:582` `OzonSellerClient.product_stocks_by_warehouse`
  - `POST https://api-seller.ozon.ru/v2/product/info/stocks-by-warehouse/fbs` (client.py:590)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "skus": {
      "type": "array",
      "items": {
        "type": "integer"
      },
      "description": "filter"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_orders_fbs

FBS orders with financial data. Cursor pagination: if has_next=true, repeat with cursor from the response (заказы FBS).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1264`.

- `ozon_mcp/client.py:707` `OzonSellerClient.posting_fbs_list`
  - `POST https://api-seller.ozon.ru/v4/posting/fbs/list` (client.py:735)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "since": {
      "type": "string"
    },
    "to": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    },
    "status": {
      "type": "string",
      "description": "awaiting_packaging, awaiting_deliver, delivering, etc."
    },
    "cursor": {
      "type": "string",
      "description": "next-page cursor from previous response"
    }
  },
  "required": [
    "since",
    "to"
  ]
}
```

## ozon_order_fbs_get

FBS posting details (детали отправления).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1271`.

- `ozon_mcp/client.py:737` `OzonSellerClient.posting_fbs_get`
  - `POST https://api-seller.ozon.ru/v3/posting/fbs/get` (client.py:739)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    }
  },
  "required": [
    "posting_number"
  ]
}
```

## ozon_order_fbs_ship

Assemble an FBS order (v4). packages: [{products: [{product_id, quantity}]}] (собрать заказ).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1273`.

- `ozon_mcp/client.py:741` `OzonSellerClient.posting_fbs_ship`
  - `POST https://api-seller.ozon.ru/v4/posting/fbs/ship` (client.py:743)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    },
    "packages": {
      "type": "array",
      "items": {
        "type": "object"
      }
    }
  },
  "required": [
    "posting_number",
    "packages"
  ]
}
```

## ozon_orders_fbs_unfulfilled

Unfulfilled FBS orders awaiting packaging: statuses awaiting_packaging and awaiting_deliver, last 30 days. Cursor pagination when has_next=true (несобранные заказы).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1275`.

- `ozon_mcp/client.py:749` `OzonSellerClient.posting_fbs_unfulfilled`
  - `POST https://api-seller.ozon.ru/v4/posting/fbs/list` (client.py:783)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 100
    },
    "cursor": {
      "type": "string",
      "description": "next-page cursor from previous response"
    }
  }
}
```

## ozon_order_fbs_label

FBS posting labels, PDF base64 (этикетки).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1280`.

- `ozon_mcp/client.py:785` `OzonSellerClient.posting_fbs_package_label`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/package-label` (client.py:787)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_numbers": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  },
  "required": [
    "posting_numbers"
  ]
}
```

## ozon_order_fbs_cancel

Cancel an FBS posting (отменить отправление).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1282`.

- `ozon_mcp/client.py:866` `OzonSellerClient.posting_fbs_cancel`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/cancel` (client.py:868)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    },
    "cancel_reason_id": {
      "type": "integer"
    },
    "cancel_reason_message": {
      "type": "string"
    }
  },
  "required": [
    "posting_number",
    "cancel_reason_id"
  ]
}
```

## ozon_order_fbs_cancel_reasons

FBS cancellation reasons (причины отмены).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1284`.

- `ozon_mcp/client.py:870` `OzonSellerClient.posting_fbs_cancel_reasons`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/cancel-reason/list` (client.py:872)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_order_fbs_act_create

Create an FBS handover act (акт приёма-передачи). DEPRECATED: Ozon switches this endpoint off on 2026-09-07 — use ozon_carriage_create plus ozon_carriage_approve.

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1286`.

- `ozon_mcp/client.py:789` `OzonSellerClient.posting_fbs_act_create`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/act/create` (client.py:794)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "containers_count": {
      "type": "integer",
      "default": 1
    }
  }
}
```

## ozon_finance_accrual_types

Accrual type reference: what each type_id in the finance tools means (справочник начислений).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1288`.

- `ozon_mcp/client.py:382` `OzonSellerClient.finance_accrual_types`
  - `POST https://api-seller.ozon.ru/v1/finance/accrual/types` (client.py:390)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_carriage_delivery_list

Delivery methods and their carriages (методы доставки, отгрузки). Source of delivery_method_id for ozon_carriage_create.

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1290`.

- `ozon_mcp/client.py:820` `OzonSellerClient.carriage_delivery_list`
  - `POST https://api-seller.ozon.ru/v2/carriage/delivery/list` (client.py:826)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    },
    "offset": {
      "type": "integer",
      "default": 0
    }
  }
}
```

## ozon_action_auto_add_products

[P0] Goods Ozon will add to a promotion by itself on a given date (автодобавление в акцию).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1293`.

- `ozon_mcp/client.py:828` `OzonSellerClient.action_auto_add_products`
  - `POST https://api-seller.ozon.ru/v1/actions/auto-add/products/list` (client.py:831)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer",
      "description": "from ozon_actions_list"
    },
    "auto_add_date": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "limit": {
      "type": "integer",
      "default": 50
    },
    "offset": {
      "type": "integer",
      "default": 0
    }
  },
  "required": [
    "action_id",
    "auto_add_date"
  ]
}
```

## ozon_action_auto_add_candidates

[P0] Candidates for automatic addition to a promotion (кандидаты на автодобавление).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1297`.

- `ozon_mcp/client.py:836` `OzonSellerClient.action_auto_add_candidates`
  - `POST https://api-seller.ozon.ru/v1/actions/auto-add/products/candidates` (client.py:839)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "auto_add_date": {
      "type": "string",
      "description": "YYYY-MM-DD"
    },
    "limit": {
      "type": "integer",
      "default": 50
    },
    "offset": {
      "type": "integer",
      "default": 0
    }
  },
  "required": [
    "action_id",
    "auto_add_date"
  ]
}
```

## ozon_action_auto_add_delete

[P0] Remove goods from automatic addition to a promotion (убрать из автодобавления).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1301`.

- `ozon_mcp/client.py:844` `OzonSellerClient.action_auto_add_delete`
  - `POST https://api-seller.ozon.ru/v1/actions/auto-add/products/delete` (client.py:846)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action_id": {
      "type": "integer"
    },
    "product_ids": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "action_id",
    "product_ids"
  ]
}
```

## ozon_carriage_create

Create an FBS carriage — the replacement for the handover act (создать отгрузку). delivery_method_id is required here on purpose: Ozon accepts an empty body and would pick postings itself, creating a real carriage.

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1304`.

- `ozon_mcp/client.py:796` `OzonSellerClient.carriage_create`
  - `POST https://api-seller.ozon.ru/v1/carriage/create` (client.py:818)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "delivery_method_id": {
      "type": "integer",
      "description": "from ozon_delivery_methods"
    },
    "departure_date": {
      "type": "string",
      "description": "RFC3339"
    },
    "containers_count": {
      "type": "integer",
      "default": 1
    }
  },
  "required": [
    "delivery_method_id"
  ]
}
```

## ozon_carriage_approve

Approve a carriage, status new to formed (подтвердить отгрузку).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1309`.

- `ozon_mcp/client.py:850` `OzonSellerClient.carriage_approve`
  - `POST https://api-seller.ozon.ru/v1/carriage/approve` (client.py:852)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "carriage_id": {
      "type": "integer"
    }
  },
  "required": [
    "carriage_id"
  ]
}
```

## ozon_order_fbs_act_status

Handover act generation status (статус акта).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1311`.

- `ozon_mcp/client.py:854` `OzonSellerClient.posting_fbs_act_check_status`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/act/check-status` (client.py:856)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "id": {
      "type": "integer"
    }
  },
  "required": [
    "id"
  ]
}
```

## ozon_order_fbs_act_pdf

Download the handover act PDF (PDF акта).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1313`.

- `ozon_mcp/client.py:858` `OzonSellerClient.posting_fbs_act_get_pdf`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/act/get-pdf` (client.py:860)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "id": {
      "type": "integer"
    }
  },
  "required": [
    "id"
  ]
}
```

## ozon_order_fbs_digital_act

Act status; digital acts were removed by Ozon 2026-03-22, the regular act is used (цифровой акт).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1315`.

- `ozon_mcp/client.py:862` `OzonSellerClient.posting_fbs_digital_act_create`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/act/check-status` (client.py:856)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "id": {
      "type": "integer"
    }
  },
  "required": [
    "id"
  ]
}
```

## ozon_order_fbs_country_list

Countries for an FBS posting (страны).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1317`.

- `ozon_mcp/client.py:874` `OzonSellerClient.posting_fbs_product_country_list`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/product/country/list` (client.py:876)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    }
  },
  "required": [
    "posting_number"
  ]
}
```

## ozon_order_fbs_country_set

Set the product country in a posting (указать страну).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1319`.

- `ozon_mcp/client.py:878` `OzonSellerClient.posting_fbs_product_country_set`
  - `POST https://api-seller.ozon.ru/v2/posting/fbs/product/country/set` (client.py:880)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    },
    "product_id": {
      "type": "integer"
    },
    "country_iso": {
      "type": "string"
    }
  },
  "required": [
    "posting_number",
    "product_id",
    "country_iso"
  ]
}
```

## ozon_order_fbs_restrictions

FBS posting restrictions (ограничения отправлений).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1321`.

- `ozon_mcp/client.py:882` `OzonSellerClient.posting_fbs_restrictions`
  - `POST https://api-seller.ozon.ru/v1/posting/fbs/restrictions` (client.py:884)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "array",
      "items": {
        "type": "string"
      }
    }
  },
  "required": [
    "posting_number"
  ]
}
```

## ozon_order_fbo_get

FBO posting details (детали FBO).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1323`.

- `ozon_mcp/client.py:886` `OzonSellerClient.posting_fbo_get`
  - `POST https://api-seller.ozon.ru/v2/posting/fbo/get` (client.py:888)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    }
  },
  "required": [
    "posting_number"
  ]
}
```

## ozon_orders_fbo

FBO orders (заказы FBO).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1327`.

- `ozon_mcp/client.py:693` `OzonSellerClient.posting_fbo_list`
  - `POST https://api-seller.ozon.ru/v3/posting/fbo/list` (client.py:700)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "since": {
      "type": "string",
      "description": "YYYY-MM-DDT00:00:00Z"
    },
    "to": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  },
  "required": [
    "since",
    "to"
  ]
}
```

## ozon_returns_fbo

Unified FBO+FBS returns list (/v1/returns/list; old returns/company/* switched off) (возвраты).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1334`.

- `ozon_mcp/client.py:918` `OzonSellerClient.returns_list`
  - `POST https://api-seller.ozon.ru/v1/returns/list` (client.py:923)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "filter": {
      "type": "object",
      "description": "filter"
    },
    "limit": {
      "type": "integer",
      "default": 100
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_returns_fbs

rFBS buyer return claims that need a seller decision (заявки на возврат).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1336`.

- `ozon_mcp/client.py:925` `OzonSellerClient.returns_rfbs_list`
  - `POST https://api-seller.ozon.ru/v2/returns/rfbs/list` (client.py:930)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_returns_fbs_get

rFBS return claim details (детали заявки).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1342`.

- `ozon_mcp/client.py:932` `OzonSellerClient.returns_rfbs_get`
  - `POST https://api-seller.ozon.ru/v2/returns/rfbs/get` (client.py:934)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "return_id": {
      "type": "integer"
    }
  },
  "required": [
    "return_id"
  ]
}
```

## ozon_returns_fbs_approve

Approve an rFBS claim (verify) (одобрить возврат).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1338`.

- `ozon_mcp/client.py:936` `OzonSellerClient.returns_rfbs_action`
  - `POST https://api-seller.ozon.ru/v2/returns/rfbs/{action}` (client.py:946)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "return_id": {
      "type": "integer"
    }
  },
  "required": [
    "return_id"
  ]
}
```

## ozon_returns_fbs_reject

Reject an rFBS claim; comment required (отклонить возврат).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1340`.

- `ozon_mcp/client.py:936` `OzonSellerClient.returns_rfbs_action`
  - `POST https://api-seller.ozon.ru/v2/returns/rfbs/{action}` (client.py:946)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "return_id": {
      "type": "integer"
    },
    "reason": {
      "type": "string"
    }
  },
  "required": [
    "return_id",
    "reason"
  ]
}
```

## ozon_returns_rfbs_action

rFBS claim action: receive-return (confirm goods received), return-money (refund), compensate (compensation without return) (действие по возврату).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1344`.

- `ozon_mcp/client.py:936` `OzonSellerClient.returns_rfbs_action`
  - `POST https://api-seller.ozon.ru/v2/returns/rfbs/{action}` (client.py:946)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "action": {
      "type": "string",
      "description": "receive-return | return-money | compensate"
    },
    "return_id": {
      "type": "integer"
    },
    "comment": {
      "type": "string"
    }
  },
  "required": [
    "action",
    "return_id"
  ]
}
```

## ozon_returns_report

Create a returns report (отчёт по возвратам).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1347`.

- `ozon_mcp/client.py:948` `OzonSellerClient.report_returns_create`
  - `POST https://api-seller.ozon.ru/v2/report/returns/create` (client.py:950)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "filter": {
      "type": "object",
      "description": "date_from, date_to etc."
    }
  },
  "required": [
    "filter"
  ]
}
```

## ozon_questions

Buyer questions (вопросы покупателей).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1351`.

- `ozon_mcp/client.py:953` `OzonSellerClient.question_list`
  - `POST https://api-seller.ozon.ru/v1/question/list` (client.py:958)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    },
    "last_id": {
      "type": "string"
    }
  }
}
```

## ozon_question_reply

Reply to a buyer question; needs the product sku (ответить на вопрос).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1353`.

- `ozon_mcp/client.py:960` `OzonSellerClient.question_reply`
  - `POST https://api-seller.ozon.ru/v1/question/answer/create` (client.py:962)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "question_id": {
      "type": "string"
    },
    "sku": {
      "type": "integer"
    },
    "text": {
      "type": "string"
    }
  },
  "required": [
    "question_id",
    "sku",
    "text"
  ]
}
```

## ozon_chat_list

Buyer chats (v3). unread_only=true for unread ones (чаты).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1357`.

- `ozon_mcp/client.py:973` `OzonSellerClient.chat_list`
  - `POST https://api-seller.ozon.ru/v3/chat/list` (client.py:982)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "unread_only": {
      "type": "boolean",
      "default": false
    },
    "page_size": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_chat_history

Chat message history (история чата).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1362`.

- `ozon_mcp/client.py:984` `OzonSellerClient.chat_history`
  - `POST https://api-seller.ozon.ru/v3/chat/history` (client.py:989)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "chat_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  },
  "required": [
    "chat_id"
  ]
}
```

## ozon_chat_send

Send a chat message (написать в чат).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1364`.

- `ozon_mcp/client.py:991` `OzonSellerClient.chat_send_message`
  - `POST https://api-seller.ozon.ru/v1/chat/send/message` (client.py:993)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "chat_id": {
      "type": "string"
    },
    "text": {
      "type": "string"
    }
  },
  "required": [
    "chat_id",
    "text"
  ]
}
```

## ozon_chat_send_file

Send a file to a chat (отправить файл).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1366`.

- `ozon_mcp/client.py:995` `OzonSellerClient.chat_send_file`
  - `POST https://api-seller.ozon.ru/v1/chat/send/file` (client.py:997)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "chat_id": {
      "type": "string"
    },
    "file_url": {
      "type": "string"
    },
    "file_name": {
      "type": "string"
    }
  },
  "required": [
    "chat_id",
    "file_url",
    "file_name"
  ]
}
```

## ozon_chat_updates

Chat updates (обновления чатов).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1368`.

- `ozon_mcp/client.py:999` `OzonSellerClient.chat_updates`
  - `POST https://api-seller.ozon.ru/v3/chat/list` (client.py:982)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_chat_start

Start a chat about a posting (начать чат).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1370`.

- `ozon_mcp/client.py:1003` `OzonSellerClient.chat_start`
  - `POST https://api-seller.ozon.ru/v1/chat/start` (client.py:1005)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string"
    }
  },
  "required": [
    "posting_number"
  ]
}
```

## ozon_chat_read

Mark a chat as read (пометить прочитанным).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1372`.

- `ozon_mcp/client.py:1007` `OzonSellerClient.chat_read`
  - `POST https://api-seller.ozon.ru/v2/chat/read` (client.py:1012)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "chat_id": {
      "type": "string"
    }
  },
  "required": [
    "chat_id"
  ]
}
```

## ozon_cancellation_list

Buyer cancellation claims (v2). state: ALL | ON_APPROVAL | APPROVED | REJECTED (заявки на отмену).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1376`.

- `ozon_mcp/client.py:1015` `OzonSellerClient.conditional_cancellation_list`
  - `POST https://api-seller.ozon.ru/v2/conditional-cancellation/list` (client.py:1027)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "posting_number": {
      "type": "string",
      "description": "filter"
    },
    "state": {
      "type": "string",
      "default": "ON_APPROVAL"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  }
}
```

## ozon_cancellation_approve

Approve a cancellation claim (одобрить отмену).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1382`.

- `ozon_mcp/client.py:1029` `OzonSellerClient.conditional_cancellation_approve`
  - `POST https://api-seller.ozon.ru/v2/conditional-cancellation/approve` (client.py:1034)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "cancellation_id": {
      "type": "integer"
    },
    "comment": {
      "type": "string"
    }
  },
  "required": [
    "cancellation_id"
  ]
}
```

## ozon_cancellation_reject

Reject a cancellation claim (отклонить отмену).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1384`.

- `ozon_mcp/client.py:1036` `OzonSellerClient.conditional_cancellation_reject`
  - `POST https://api-seller.ozon.ru/v2/conditional-cancellation/reject` (client.py:1038)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "cancellation_id": {
      "type": "integer"
    },
    "comment": {
      "type": "string"
    }
  },
  "required": [
    "cancellation_id"
  ]
}
```

## ozon_warehouse_list

Seller FBS warehouses (склады).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1388`.

- `ozon_mcp/client.py:1041` `OzonSellerClient.warehouse_list`
  - `POST https://api-seller.ozon.ru/v2/warehouse/list` (client.py:1043)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "view": {
      "type": "string",
      "enum": [
        "compact",
        "full"
      ],
      "description": "compact (default) trims heavy fields; full returns the raw API response"
    }
  }
}
```

## ozon_delivery_methods

Delivery methods (методы доставки).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1390`.

- `ozon_mcp/client.py:1045` `OzonSellerClient.delivery_method_list`
  - `POST https://api-seller.ozon.ru/v2/delivery-method/list` (client.py:1047)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "default": 50
    }
  }
}
```

## ozon_report_list

Generated reports (список отчётов).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1394`.

- `ozon_mcp/client.py:1050` `OzonSellerClient.report_list`
  - `POST https://api-seller.ozon.ru/v1/report/list` (client.py:1055)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "report_type": {
      "type": "string"
    },
    "page": {
      "type": "integer",
      "default": 1
    }
  }
}
```

## ozon_report_info

Report status and download link (статус отчёта).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1396`.

- `ozon_mcp/client.py:1057` `OzonSellerClient.report_info`
  - `POST https://api-seller.ozon.ru/v1/report/info` (client.py:1059)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "code": {
      "type": "string"
    }
  },
  "required": [
    "code"
  ]
}
```

## ozon_report_products_create

Create a products report (отчёт по товарам).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1398`.

- `ozon_mcp/client.py:1061` `OzonSellerClient.report_products_create`
  - `POST https://api-seller.ozon.ru/v1/report/products/create` (client.py:1063)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "visibility": {
      "type": "string",
      "default": "ALL"
    }
  }
}
```

## ozon_report_stocks_create

Create a stock report (отчёт по остаткам).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1400`.

- `ozon_mcp/client.py:1065` `OzonSellerClient.report_stocks_create`
  - `POST https://api-seller.ozon.ru/v1/analytics/turnover/stocks` (client.py:528)
  - `POST https://api-seller.ozon.ru/v1/report/warehouse/stock` (client.py:1072)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_report_finance_create

Create a financial report (финансовый отчёт).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1402`.

- `ozon_mcp/client.py:1075` `OzonSellerClient.report_finance_create`
  - `POST https://api-seller.ozon.ru/v1/finance/cash-flow-statement/list` (client.py:448)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "date_from": {
      "type": "string"
    },
    "date_to": {
      "type": "string"
    }
  },
  "required": [
    "date_from",
    "date_to"
  ]
}
```

## ozon_report_discounted_create

Report on discounted goods (отчёт по уценке).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1404`.

- `ozon_mcp/client.py:1079` `OzonSellerClient.report_discounted_create`
  - `POST https://api-seller.ozon.ru/v1/report/discounted/create` (client.py:1081)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate; Report generation creates a remote job/artifact; counted as WRITE conservatively, not business inventory mutation

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_brand_certificates

Brand certificates (сертификаты бренда).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1408`.

- `ozon_mcp/client.py:1084` `OzonSellerClient.brand_company_certification_list`
  - `POST https://api-seller.ozon.ru/v1/brand/company-certification/list` (client.py:1086)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_category_tree

Ozon category tree (дерево категорий). Whole tree is 9 800 nodes: pass search to find a category, or depth to go deeper than top level.

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1412`.

- `ozon_mcp/client.py:1089` `OzonSellerClient.description_category_tree`
  - `POST https://api-seller.ozon.ru/v1/description-category/tree` (client.py:1091)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "search": {
      "type": "string",
      "description": "category name substring, case-insensitive (название категории)"
    },
    "depth": {
      "type": "integer",
      "default": 1,
      "description": "1 = top level only, 3 = whole tree"
    }
  }
}
```

## ozon_category_attributes

Category attributes (атрибуты категории).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1417`.

- `ozon_mcp/client.py:1093` `OzonSellerClient.description_category_attribute`
  - `POST https://api-seller.ozon.ru/v1/description-category/attribute` (client.py:1095)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "description_category_id": {
      "type": "integer"
    },
    "type_id": {
      "type": "integer",
      "default": 0
    }
  },
  "required": [
    "description_category_id"
  ]
}
```

## ozon_category_attribute_values

Category attribute values (значения атрибута).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1419`.

- `ozon_mcp/client.py:1097` `OzonSellerClient.description_category_attribute_values`
  - `POST https://api-seller.ozon.ru/v1/description-category/attribute/values` (client.py:1099)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "description_category_id": {
      "type": "integer"
    },
    "attribute_id": {
      "type": "integer"
    },
    "limit": {
      "type": "integer",
      "default": 100
    }
  },
  "required": [
    "description_category_id",
    "attribute_id"
  ]
}
```

## ozon_category_attribute_search

Search attribute values (поиск значений).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1421`.

- `ozon_mcp/client.py:1101` `OzonSellerClient.description_category_attribute_values_search`
  - `POST https://api-seller.ozon.ru/v1/description-category/attribute/values/search` (client.py:1103)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "description_category_id": {
      "type": "integer"
    },
    "attribute_id": {
      "type": "integer"
    },
    "value": {
      "type": "string"
    }
  },
  "required": [
    "description_category_id",
    "attribute_id",
    "value"
  ]
}
```

## ozon_notifications

Push notification subscriptions (webhooks). Ozon has no notification list endpoint (уведомления, вебхуки).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1425`.

- `ozon_mcp/client.py:1106` `OzonSellerClient.notification_list`
  - `POST https://api-seller.ozon.ru/v1/notification/list` (client.py:1108)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_notification_push_types

Push event types reference: new messages, posting statuses (типы событий).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1427`.

- `ozon_mcp/client.py:1110` `OzonSellerClient.notification_push_types`
  - `POST https://api-seller.ozon.ru/v1/notification/push-type/list` (client.py:1112)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_discount_tasks

Buyer 'want a discount' requests. status: NEW | SEEN | APPROVED | PARTLY_APPROVED | DECLINED | AUTO_DECLINED (заявки на скидку).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1447`.

- `ozon_mcp/client.py:1119` `OzonSellerClient.discount_task_list`
  - `POST https://api-seller.ozon.ru/v2/actions/discounts-task/list` (client.py:1124)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "status": {
      "type": "string",
      "default": "NEW"
    },
    "limit": {
      "type": "integer",
      "default": 50,
      "description": "5/10/15/20/30/50"
    }
  }
}
```

## ozon_discount_approve

Approve discount requests (одобрить скидку).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1450`.

- `ozon_mcp/client.py:1126` `OzonSellerClient.discount_task_approve`
  - `POST https://api-seller.ozon.ru/v1/actions/discounts-task/approve` (client.py:1132)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "tasks": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "[{id, approved_price, seller_comment, approved_quantity_min, approved_quantity_max}]"
    }
  },
  "required": [
    "tasks"
  ]
}
```

## ozon_discount_decline

Decline discount requests (отклонить скидку).

Classification: **WRITE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1452`.

- `ozon_mcp/client.py:1134` `OzonSellerClient.discount_task_decline`
  - `POST https://api-seller.ozon.ru/v1/actions/discounts-task/decline` (client.py:1136)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "tasks": {
      "type": "array",
      "items": {
        "type": "object"
      },
      "description": "[{id, seller_comment}]"
    }
  },
  "required": [
    "tasks"
  ]
}
```

## ozon_company_info

Seller company info (информация о компании).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1456`.

- `ozon_mcp/client.py:1139` `OzonSellerClient.company_info`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Local unsupported-endpoint stub; no data request, not a working read capability

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_company_tariffs

Company tariffs (тарифы).

Classification: **READ_SENSITIVE**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1458`.

- `ozon_mcp/client.py:1142` `OzonSellerClient.company_tariffs`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Local unsupported-endpoint stub; no data request, not a working read capability

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_certificate_list

All certificates (сертификаты).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1462`.

- `ozon_mcp/client.py:1146` `OzonSellerClient.certificate_list`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Local unsupported-endpoint stub; no data request, not a working read capability

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "status": {
      "type": "string"
    }
  }
}
```

## ozon_certificate_info

Certificate details (детали сертификата).

Classification: **SAFE_READ**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1464`.

- `ozon_mcp/client.py:1149` `OzonSellerClient.certificate_info`

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Local unsupported-endpoint stub; no data request, not a working read capability

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "certificate_id": {
      "type": "integer"
    }
  },
  "required": [
    "certificate_id"
  ]
}
```

## ozon_product_archive

Move goods to archive (архивировать товары).

Classification: **DESTRUCTIVE_OR_HIGH_RISK**. Family: Seller API. Credentials: Seller Client-Id + Api-Key.

Dispatch: `ozon_mcp/server.py:1468`.

- `ozon_mcp/client.py:1153` `OzonSellerClient.product_archive`
  - `POST https://api-seller.ozon.ru/v1/product/archive` (client.py:1155)

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Excluded from V1; no upstream per-operation approval gate

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    },
    "product_id": {
      "type": "array",
      "items": {
        "type": "integer"
      }
    }
  },
  "required": [
    "product_id"
  ]
}
```

## ozon_diagnostics

[P0] Full self-diagnostics: Ozon host availability, light real requests across 12 Seller API categories, Performance API key check. Run FIRST when a tool misbehaves — separates a key problem from a category or Ozon API change (диагностика).

Classification: **READ_SENSITIVE**. Family: Seller API, Performance API. Credentials: Seller Client-Id + Api-Key; optional Performance client_id/client_secret.

Dispatch: `ozon_mcp/server.py:989`.

diagnostics.py:full_diagnostics -> 2 host GETs + 12 parallel Seller probes (financial probe expands to multiple requests) + POST /v1/roles + optional Performance token POST. Typical cold-cache two-day one-page financial probe: 18 initial calls with Performance; actual count varies with pagination/cache/errors/retries.

Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use

Active diagnostics, not passive health; detailed network map in network-map.md

Input schema:
```json
{
  "type": "object",
  "properties": {
    "shop_id": {
      "type": "string"
    }
  }
}
```

## ozon_degradations

[P0] Tool degradations: which MCP tools used to work and now fail steadily, signalling an Ozon API change. No parameters (деградации).

Classification: **READ_SENSITIVE**. Family: Local. Credentials: None for local tools.

Dispatch: `ozon_mcp/server.py:956`.


Exact Ozon role/capability mapping not encoded by upstream; verify official documentation before use



Input schema:
```json
{
  "type": "object",
  "properties": {}
}
```
