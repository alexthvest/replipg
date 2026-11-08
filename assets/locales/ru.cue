package locales

ru: {
	"command.ping.message": "Понг!"

	"command.replipg.usage":       "Используйте /replipg balance, чтобы проверить баланс бота в банке."
	"command.replipg.unavailable": "Баланс банка недоступен. Попробуйте позже."
	"command.replipg.balance":     "Баланс бота в банке (счёт {account}): {amount} ∅"

	"command.admin.usage":   "Ответьте игроку командой /admin add или /admin remove."
	"command.admin.updated": "Права игрового администратора обновлены для {user}."

	"menu.settings.title":    "Настройки"
	"menu.settings.language": "Язык"
	"language.en":            "Английский"
	"language.ru":            "Русский"

	"command.character.usage": "Используйте /character для просмотра своего персонажа в этой группе."
	"menu.character.title":    "Персонаж"
	"menu.character.name":     "Имя"
	"menu.character.id":       "ID"
	"error.name":              "Используйте /rename и имя длиной от 1 до 64 символов."

	"command.give.usage":   "Ответьте игроку командой /give <item_id> <quantity>; количество — целое число от 1 до 1000."
	"command.give.failed":  "Не удалось выдать предметы. Проверьте получателя, активный ID предмета, количество и хранилище."
	"command.give.success": "Выдано {quantity} × {item} игроку {recipient}."

	"command.inventory.usage": "Используйте /inventory для просмотра своего инвентаря в этой группе."
	"menu.inventory.title":    "Инвентарь"
	"menu.inventory.empty":    "Ваш инвентарь пуст."
	"menu.inventory.page":     "Страница"

	"command.items.usage": "Используйте /items для просмотра всех доступных предметов игры."
	"command.items.given": "Добавлено 1 × {item} в ваш инвентарь."
	"menu.items.title":    "Предметы"
	"menu.items.empty":    "Нет доступных предметов."
	"menu.items.page":     "Страница"

	"command.shop.usage": "Используйте /shop для покупок, продаж и просмотра заказов в этой группе."

	"menu.shop.title":              "Магазин"
	"menu.shop.buy":                "Купить"
	"menu.shop.sell":               "Продать"
	"menu.shop.orders":             "Мои заказы"
	"menu.shop.empty":              "Подходящих предметов нет."
	"menu.shop.no_orders":          "В этой группе у вас нет заказов."
	"menu.shop.quantity":           "Количество"
	"menu.shop.unit":               "За штуку"
	"menu.shop.total":              "Итого"
	"menu.shop.available":          "Доступно"
	"menu.shop.review":             "Проверка заказа"
	"menu.shop.confirm":            "Подтвердить"
	"menu.shop.changed":            "Условия изменились. Проверьте и подтвердите обновлённый заказ."
	"menu.shop.unavailable":        "Банк недоступен."
	"menu.shop.failed":             "Не удалось создать заказ. Проверьте предложение и доступное количество."
	"menu.shop.refresh":            "Обновить"
	"menu.shop.max":                "Макс"
	"menu.shop.owned":              "Всего"
	"menu.shop.reserved":           "В резерве"
	"menu.shop.sale_available":     "Для продажи"
	"menu.shop.account_help":       "Банк не смог выполнить операцию. Проверьте, есть ли у вас USER-счёт в банке сообщества; оператору также может потребоваться проверить доступ к банку и средства казны."
	"menu.shop.buyer_funds":        "На вашем банковском счёте не хватило средств для оплаты счёта. Пополните его перед новым заказом на покупку."
	"menu.shop.buyer_refused":      "Вы отказались от оплаты счёта. Новый платёж автоматически не создаётся."
	"menu.shop.unresolved_hold":    "Расчёт не подтверждён; зарезервированные предметы остаются заблокированными, пока сверяется исходная операция."
	"menu.shop.unresolved_payment": "Расчёт не подтверждён; сверяется исходная платёжная операция. Новый счёт не создаётся."

	"menu.order.title":                   "Заказ"
	"menu.order.status":                  "Статус"
	"menu.order.created":                 "Подготовка счёта"
	"menu.order.requesting":              "Запрос счёта"
	"menu.order.awaiting-payment":        "Ожидает оплаты"
	"menu.order.paid":                    "Оплачен; ожидает выдачи предметов"
	"menu.order.reserved":                "Предметы зарезервированы"
	"menu.order.payout-requesting":       "Ожидает выплаты; предметы зарезервированы"
	"menu.order.payout-confirmed":        "Выплата подтверждена; завершение продажи"
	"menu.order.completed":               "Завершён"
	"menu.order.rejected":                "Отклонён без оплаты"
	"menu.order.failed":                  "Завершён без оплаты"
	"menu.order.reconciliation-required": "Результат требует сверки; сохранён исходный запрос"

	"menu.item.reserved":  "Зарезервировано"
	"menu.item.available": "Доступно"

	"menu.item.title":       "Предмет"
	"menu.item.name":        "Название"
	"menu.item.description": "Описание"
	"menu.item.rarity":      "Редкость"
	"menu.item.mode":        "Владение"
	"menu.item.components":  "Компоненты"
	"menu.item.quantity":    "Количество"
	"menu.item.instance":    "Экземпляр"

	"menu.back":     "Назад"
	"menu.previous": "Предыдущая"
	"menu.next":     "Следующая"

	"error.denied":       "У вас нет прав для этого действия."
	"error.invalid_menu": "Меню недоступно. Откройте его командой заново."
	"error.persistence":  "Не удалось сохранить изменения. Попробуйте ещё раз."

	"rarity.common":    "Обычный"
	"rarity.uncommon":  "Необычный"
	"rarity.rare":      "Редкий"
	"rarity.epic":      "Эпический"
	"rarity.legendary": "Легендарный"

	"mode.stackable":  "Складываемый"
	"mode.individual": "Отдельный"

	"item.component.none":            "Нет"
	"item.component.consumable.name": "Расходуемый"
	"item.component.material.name":   "Материал"
	"item.component.weapon.name":     "Оружие"

	"item.coin.name":        "Монета"
	"item.coin.description": "Обычная монета."

	"item.stick.name":        "Палка"
	"item.stick.description": "Обычная деревянная палка."

	"item.fumo.name":        "Фумо"
	"item.fumo.description": "Мягкая сидячая плюшевая игрушка с вышитым лицом. Тихий спутник в ваших приключениях."

	"item.dakimakura.name":        "Дакимакура"
	"item.dakimakura.description": "Подушка в полный рост с любимым персонажем аниме. Занимает половину кровати и всё ваше сердце."

	"item.neco_arc.name":        "Неко-Арк"
	"item.neco_arc.description": "Крошечное кошачье создание с огромным талантом к хаосу. Вы её не заводили. Она теперь просто здесь живёт."

	"item.bread.name":        "Хлеб"
	"item.bread.description": "Круглая буханка с хрустящей золотистой корочкой."

	"item.iron_sword.name":        "Железный меч"
	"item.iron_sword.description": "Простой железный клинок с рукоятью, обмотанной кожей."

	"item.silver_ring.name":        "Серебряное кольцо"
	"item.silver_ring.description": "Полированное серебряное кольцо с гравировкой в виде маленьких листьев."

	"item.ruby.name":        "Рубин"
	"item.ruby.description": "Драгоценный камень насыщенного красного цвета, сверкающий на свету."
}
