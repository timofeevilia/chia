// Minimal two-language dictionary. `t(key)` is reactive: changing the locale
// re-renders every string.
const en = {
	tagline: 'Split bills across currencies. Settle with one QR scan.',
	intro: 'Record who paid for what, in dong, Singapore dollars or anything else. Chia works out who owes whom and makes a VietQR or PayNow code to pay them back.',
	noSignup: 'No sign-up. Share the link with your group.',
	createGroup: 'Create a group',
	groupName: 'Group name',
	groupNamePh: 'Da Nang trip',
	baseCurrency: 'Settle in',
	members: 'Members',
	memberNamePh: 'Name',
	add: 'Add',
	create: 'Create group',
	needTwo: 'Add at least two people.',
	recent: 'Your recent groups',
	howTitle: 'How it works',
	how1: 'Create a group and send the link to your friends.',
	how2: 'Add expenses in any currency. The exchange rate is fixed when you add them.',
	how3: 'See the fewest payments that settle everyone, and pay by QR.',
	disclaimer: 'Chia never holds or moves money. It keeps the books and prepares the payment request.',
	apiDocs: 'API docs',
	source: 'Source code',
	copyLink: 'Copy invite link',
	copied: 'Link copied',
	balances: 'Balances',
	getsBack: 'gets back',
	owes: 'owes',
	settled: 'settled',
	allSettled: 'Everyone is settled up.',
	toSettle: 'To settle up',
	pays: 'pays',
	payQr: 'Pay by QR',
	markPaid: 'Mark as paid',
	addExpense: 'Add an expense',
	description: 'What was it for?',
	descriptionPh: 'Dinner, taxi, hotel…',
	amount: 'Amount',
	currency: 'Currency',
	paidBy: 'Paid by',
	splitBetween: 'Split between',
	rate: 'Exchange rate',
	rateHint: '1 {from} = ? {to}. Leave empty to use today’s rate ({rate}).',
	save: 'Add expense',
	saving: 'Saving…',
	invalidAmount: 'Enter a valid amount.',
	pickSomeone: 'Choose at least one person to split between.',
	activity: 'Activity',
	noActivity: 'No expenses yet. Add the first one above.',
	paid: 'paid',
	paidTo: 'paid',
	undo: 'Undo',
	undoConfirm: 'Cancel this entry? The ledger keeps a record of the cancellation.',
	cancelled: 'cancelled',
	cancellation: 'Cancellation',
	loadMore: 'Load more',
	addMember: 'Add a member',
	payout: 'Payment details',
	payoutNone: 'No payment details',
	payoutSet: 'Set up how to get paid',
	method: 'Method',
	bank: 'Bank',
	account: 'Account number',
	mobile: 'Mobile number',
	uen: 'UEN',
	proxyType: 'PayNow ID',
	saveDetails: 'Save',
	cancel: 'Cancel',
	edit: 'Edit',
	close: 'Close',
	qrTitle: 'Pay {name}',
	qrScan: 'Scan with your banking app. Check the recipient and amount before you confirm.',
	qrConverted: 'Converted at 1 {from} = {rate} {to}.',
	qrNoPayout: '{name} has not added payment details yet. Add them under Members.',
	qrPaid: 'I have paid',
	notFound: 'This group does not exist. Check the link.',
	offline: 'Cannot reach the server. Check your connection and try again.',
	waking: 'Loading… the server may take up to a minute to wake up.',
	retry: 'Retry',
	rateLimited: 'Too many requests. Wait a minute and try again.',
	nameTaken: 'Someone in the group already has this name.',
	invalidPayout: 'These payment details do not look right.',
	failed: 'Something went wrong. Try again.'
};

const vi: typeof en = {
	tagline: 'Chia hoá đơn nhiều loại tiền. Thanh toán bằng một lần quét QR.',
	intro: 'Ghi lại ai đã trả khoản nào, bằng đồng, đô la Singapore hay tiền tệ khác. Chia tính ai nợ ai và tạo mã VietQR hoặc PayNow để trả lại.',
	noSignup: 'Không cần đăng ký. Gửi đường link cho nhóm của bạn.',
	createGroup: 'Tạo nhóm',
	groupName: 'Tên nhóm',
	groupNamePh: 'Chuyến đi Đà Nẵng',
	baseCurrency: 'Thanh toán bằng',
	members: 'Thành viên',
	memberNamePh: 'Tên',
	add: 'Thêm',
	create: 'Tạo nhóm',
	needTwo: 'Hãy thêm ít nhất hai người.',
	recent: 'Nhóm gần đây của bạn',
	howTitle: 'Cách hoạt động',
	how1: 'Tạo nhóm và gửi link cho bạn bè.',
	how2: 'Thêm chi tiêu bằng bất kỳ loại tiền nào. Tỷ giá được cố định khi bạn thêm.',
	how3: 'Xem số lần chuyển khoản ít nhất để mọi người hết nợ, và trả bằng QR.',
	disclaimer: 'Chia không giữ và không chuyển tiền. Ứng dụng chỉ ghi sổ và tạo yêu cầu thanh toán.',
	apiDocs: 'Tài liệu API',
	source: 'Mã nguồn',
	copyLink: 'Sao chép link mời',
	copied: 'Đã sao chép link',
	balances: 'Số dư',
	getsBack: 'được nhận lại',
	owes: 'còn nợ',
	settled: 'đã xong',
	allSettled: 'Mọi người đã thanh toán xong.',
	toSettle: 'Cần thanh toán',
	pays: 'trả cho',
	payQr: 'Trả bằng QR',
	markPaid: 'Đánh dấu đã trả',
	addExpense: 'Thêm chi tiêu',
	description: 'Chi cho việc gì?',
	descriptionPh: 'Ăn tối, taxi, khách sạn…',
	amount: 'Số tiền',
	currency: 'Loại tiền',
	paidBy: 'Người trả',
	splitBetween: 'Chia cho',
	rate: 'Tỷ giá',
	rateHint: '1 {from} = ? {to}. Để trống để dùng tỷ giá hôm nay ({rate}).',
	save: 'Thêm chi tiêu',
	saving: 'Đang lưu…',
	invalidAmount: 'Hãy nhập số tiền hợp lệ.',
	pickSomeone: 'Chọn ít nhất một người để chia.',
	activity: 'Hoạt động',
	noActivity: 'Chưa có chi tiêu nào. Hãy thêm khoản đầu tiên ở trên.',
	paid: 'đã trả',
	paidTo: 'đã trả cho',
	undo: 'Hoàn tác',
	undoConfirm: 'Huỷ mục này? Sổ cái vẫn lưu lại việc huỷ.',
	cancelled: 'đã huỷ',
	cancellation: 'Huỷ',
	loadMore: 'Xem thêm',
	addMember: 'Thêm thành viên',
	payout: 'Thông tin nhận tiền',
	payoutNone: 'Chưa có thông tin nhận tiền',
	payoutSet: 'Thiết lập cách nhận tiền',
	method: 'Phương thức',
	bank: 'Ngân hàng',
	account: 'Số tài khoản',
	mobile: 'Số điện thoại',
	uen: 'UEN',
	proxyType: 'Mã PayNow',
	saveDetails: 'Lưu',
	cancel: 'Huỷ',
	edit: 'Sửa',
	close: 'Đóng',
	qrTitle: 'Trả cho {name}',
	qrScan: 'Quét bằng ứng dụng ngân hàng. Kiểm tra người nhận và số tiền trước khi xác nhận.',
	qrConverted: 'Quy đổi theo tỷ giá 1 {from} = {rate} {to}.',
	qrNoPayout: '{name} chưa thêm thông tin nhận tiền. Hãy thêm trong mục Thành viên.',
	qrPaid: 'Tôi đã trả',
	notFound: 'Nhóm này không tồn tại. Hãy kiểm tra lại link.',
	offline: 'Không kết nối được máy chủ. Kiểm tra mạng và thử lại.',
	waking: 'Đang tải… máy chủ có thể mất đến một phút để khởi động.',
	retry: 'Thử lại',
	rateLimited: 'Quá nhiều yêu cầu. Chờ một phút rồi thử lại.',
	nameTaken: 'Trong nhóm đã có người dùng tên này.',
	invalidPayout: 'Thông tin nhận tiền có vẻ chưa đúng.',
	failed: 'Đã có lỗi xảy ra. Hãy thử lại.'
};

export type Key = keyof typeof en;
export type Locale = 'en' | 'vi';
const dict = { en, vi };

function initial(): Locale {
	try {
		const saved = localStorage.getItem('chia.locale');
		if (saved === 'en' || saved === 'vi') return saved;
	} catch {
		/* ignore */
	}
	return typeof navigator !== 'undefined' && navigator.language?.startsWith('vi') ? 'vi' : 'en';
}

export const i18n = $state({ locale: initial() });

export function setLocale(l: Locale) {
	i18n.locale = l;
	try {
		localStorage.setItem('chia.locale', l);
	} catch {
		/* ignore */
	}
}

export function t(key: Key, vars: Record<string, string> = {}): string {
	return dict[i18n.locale][key].replace(/\{(\w+)\}/g, (_, k) => vars[k] ?? '');
}

/** BCP 47 tag for number and date formatting. */
export function fmtLocale(): string {
	return i18n.locale === 'vi' ? 'vi-VN' : 'en-SG';
}
