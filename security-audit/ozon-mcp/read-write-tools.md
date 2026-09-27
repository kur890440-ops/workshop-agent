# READ / WRITE policy

Classification follows actual dispatched endpoints. SAFE_READ does not mean public information. DESTRUCTIVE_OR_HIGH_RISK is included in total WRITE. Report-creation tools are conservatively WRITE even when intended for reading; ozon_report_finance_create and ozon_order_fbs_digital_act actually only read. ozon_pricing_strategy_products is mixed and is WRITE. Five unsupported stubs remain in read counts; do not treat them as working API methods.

## READ_SENSITIVE (48)

- `ozon_list_shops`
- `ozon_finance_transactions`
- `ozon_finance_totals`
- `ozon_finance_realization`
- `ozon_finance_mutual_settlement`
- `ozon_finance_accruals`
- `ozon_finance_balance`
- `ozon_finance_cash_flow`
- `ozon_reviews`
- `ozon_review_comments`
- `ozon_ad_campaign_budget`
- `ozon_ad_statistics_daily`
- `ozon_ad_statistics_expenses`
- `ozon_ad_statistics_products`
- `ozon_ad_balance`
- `ozon_supply_orders`
- `ozon_supply_order_get`
- `ozon_supply_order_counters`
- `ozon_supply_order_timeslots`
- `ozon_orders_fbs`
- `ozon_order_fbs_get`
- `ozon_orders_fbs_unfulfilled`
- `ozon_order_fbs_label`
- `ozon_order_fbs_cancel_reasons`
- `ozon_finance_accrual_types`
- `ozon_order_fbs_act_status`
- `ozon_order_fbs_act_pdf`
- `ozon_order_fbs_digital_act`
- `ozon_order_fbs_country_list`
- `ozon_order_fbs_restrictions`
- `ozon_order_fbo_get`
- `ozon_orders_fbo`
- `ozon_returns_fbo`
- `ozon_returns_fbs`
- `ozon_returns_fbs_get`
- `ozon_questions`
- `ozon_chat_list`
- `ozon_chat_history`
- `ozon_chat_updates`
- `ozon_cancellation_list`
- `ozon_report_list`
- `ozon_report_info`
- `ozon_report_finance_create`
- `ozon_discount_tasks`
- `ozon_company_info`
- `ozon_company_tariffs`
- `ozon_diagnostics`
- `ozon_degradations`

## SAFE_READ (51)

- `ozon_actions_list`
- `ozon_actions_candidates`
- `ozon_actions_products`
- `ozon_seller_actions`
- `ozon_seller_action_products`
- `ozon_pricing_strategy_list`
- `ozon_pricing_strategy_info`
- `ozon_pricing_competitors`
- `ozon_pricing_competitor_prices`
- `ozon_get_prices`
- `ozon_get_prices_v4`
- `ozon_min_price_timer_status`
- `ozon_rating_summary`
- `ozon_rating_history`
- `ozon_ad_campaigns`
- `ozon_ad_campaign_objects`
- `ozon_ad_campaign_products`
- `ozon_ad_bids_competitive`
- `ozon_ad_min_bids`
- `ozon_search_promo_products`
- `ozon_search_promo_bids`
- `ozon_analytics`
- `ozon_stock_on_warehouses`
- `ozon_analytics_stocks`
- `ozon_product_queries`
- `ozon_search_queries_top`
- `ozon_product_list`
- `ozon_product_info`
- `ozon_product_attributes`
- `ozon_product_stocks`
- `ozon_product_certificates`
- `ozon_product_import_info`
- `ozon_product_description`
- `ozon_product_limits`
- `ozon_product_rating_by_sku`
- `ozon_product_discounted`
- `ozon_product_stocks_by_warehouse`
- `ozon_carriage_delivery_list`
- `ozon_action_auto_add_products`
- `ozon_action_auto_add_candidates`
- `ozon_warehouse_list`
- `ozon_delivery_methods`
- `ozon_brand_certificates`
- `ozon_category_tree`
- `ozon_category_attributes`
- `ozon_category_attribute_values`
- `ozon_category_attribute_search`
- `ozon_notifications`
- `ozon_notification_push_types`
- `ozon_certificate_list`
- `ozon_certificate_info`

## WRITE (36)

- `ozon_actions_activate`
- `ozon_actions_deactivate`
- `ozon_seller_action_create`
- `ozon_seller_action_toggle`
- `ozon_seller_action_products_add`
- `ozon_seller_action_products_delete`
- `ozon_pricing_strategy_create`
- `ozon_pricing_strategy_update`
- `ozon_pricing_strategy_status`
- `ozon_pricing_strategy_products`
- `ozon_min_price_timer_renew`
- `ozon_review_reply`
- `ozon_review_reply_delete`
- `ozon_ad_statistics`
- `ozon_ad_campaign_stop`
- `ozon_ad_products_add`
- `ozon_ad_products_delete`
- `ozon_search_promo_disable`
- `ozon_product_import`
- `ozon_product_update_offer_id`
- `ozon_product_update_images`
- `ozon_product_unarchive`
- `ozon_product_attributes_update`
- `ozon_product_import_by_sku`
- `ozon_action_auto_add_delete`
- `ozon_order_fbs_country_set`
- `ozon_returns_report`
- `ozon_question_reply`
- `ozon_chat_send`
- `ozon_chat_send_file`
- `ozon_chat_start`
- `ozon_chat_read`
- `ozon_report_products_create`
- `ozon_report_stocks_create`
- `ozon_report_discounted_create`
- `ozon_discount_decline`

## DESTRUCTIVE_OR_HIGH_RISK (21)

- `ozon_pricing_strategy_delete`
- `ozon_set_prices`
- `ozon_ad_campaign_create`
- `ozon_ad_campaign_activate`
- `ozon_ad_campaign_bids`
- `ozon_ad_campaign_budget_update`
- `ozon_search_promo_enable`
- `ozon_product_update_stocks`
- `ozon_product_delete`
- `ozon_order_fbs_ship`
- `ozon_order_fbs_cancel`
- `ozon_order_fbs_act_create`
- `ozon_carriage_create`
- `ozon_carriage_approve`
- `ozon_returns_fbs_approve`
- `ozon_returns_fbs_reject`
- `ozon_returns_rfbs_action`
- `ozon_cancellation_approve`
- `ozon_cancellation_reject`
- `ozon_discount_approve`
- `ozon_product_archive`

## OZON_WRITE_TOOLS_NOT_FOR_V1

All WRITE and DESTRUCTIVE_OR_HIGH_RISK entries above. Future V1 WRITE TOOLS = 0. No advertisement, report creation, price/stock edits, shipment/cancellation, customer replies, refunds, media or card mutation.